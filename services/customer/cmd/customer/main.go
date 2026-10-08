package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/grpcserver"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/health"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	platformkafka "github.com/zima-flow/go-mentor/mini-fintech/platform/kafka"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/logger"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/grpc"
	kafkaadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/kafka"
	metricsadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/metrics"
	customerpostgres "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/config"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/migrations"
)

const (
	serviceName     = "customer"
	otelShutdownTTL = 5 * time.Second

	reflectionV1Method      = "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"
	reflectionV1AlphaMethod = "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate", false, "apply migrations and exit")
	flag.Parse()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, serviceName)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var providers *otel.Providers
	if !*migrateOnly {
		providers, err = otel.Setup(ctx, otel.Config{
			Endpoint:    cfg.OTLPEndpoint,
			ServiceName: serviceName,
			Insecure:    true,
		})
		if err != nil {
			return fmt.Errorf("set up otel: %w", err)
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), otelShutdownTTL)
			defer cancel()
			if err := providers.Shutdown(shutdownCtx); err != nil {
				log.Warn("otel shutdown", "error", err)
			}
		}()
	}

	var dbTracer trace.Tracer
	if providers != nil {
		dbTracer = providers.TracerProvider.Tracer(serviceName)
	}
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.DatabaseURL, Tracer: dbTracer})
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if *migrateOnly {
		if err := migrate.Up(ctx, pool, migrations.FS, "."); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	}

	clk := clock.Real{}
	ids := id.UUIDv7{}
	tx := customerpostgres.NewTransactor(pool)
	customers := customerpostgres.NewCustomerRepo(pool)
	metricsRecorder := metricsadapter.NewRecorder(providers.MeterProvider.Meter(serviceName))

	customerSrv := grpcadapter.NewServer(grpcadapter.UseCases{
		GetProfile:        app.NewGetProfileUseCase(customers),
		UpdateProfile:     app.NewUpdateProfileUseCase(tx, clk, ids, metricsRecorder),
		GetCustomer:       app.NewGetCustomerUseCase(customers),
		GetCustomerStatus: app.NewGetCustomerStatusUseCase(customers),
		ListCustomers:     app.NewListCustomersUseCase(customers),
	}, providers.Propagator)

	readiness := health.New(health.Probe{Name: "postgres", Check: pool.Ping})

	grpcSrv := grpcserver.New(grpcserver.Config{
		Address:         cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Interceptors: interceptors.Config{
			Logger:     log,
			Meter:      providers.MeterProvider.Meter(serviceName),
			Tracer:     providers.TracerProvider.Tracer(serviceName),
			Propagator: providers.Propagator,
			RequestID:  ids,
			Auth: interceptors.AuthConfig{
				JWKSURL:  cfg.AuthJWKSURL,
				Issuer:   cfg.AuthIssuer,
				Audience: cfg.AuthAudience,
				Dev:      cfg.AuthDev,
				PublicMethods: []string{
					reflectionV1Method,
					reflectionV1AlphaMethod,
				},
			},
		},
		Register: func(reg grpc.ServiceRegistrar) {
			grpcadapter.Register(reg, customerSrv)

			if server, ok := reg.(reflection.GRPCServer); ok {
				reflection.Register(server)
			}
		},
		OnListen: func(addr net.Addr) {
			log.Info("listening", "transport", "grpc", "addr", addr.String())
		},
	}, log, readiness)

	producerClient, err := platformkafka.NewProducer(platformkafka.Config{
		Brokers:  cfg.BrokerList(),
		ClientID: serviceName,
	})
	if err != nil {
		return fmt.Errorf("build kafka producer: %w", err)
	}
	defer producerClient.Close()

	relay := outbox.NewRelay(outbox.RelayConfig{
		DB:         pool,
		Publisher:  kafkaadapter.NewProducer(producerClient),
		Clock:      clk,
		Logger:     log,
		Tracer:     providers.TracerProvider.Tracer(serviceName),
		Propagator: providers.Propagator,
	})

	consumerClient, err := platformkafka.NewConsumer(platformkafka.Config{
		Brokers:  cfg.BrokerList(),
		ClientID: serviceName,
		GroupID:  cfg.KafkaGroup,
		Topics:   []string{events.TopicAuth},
	})
	if err != nil {
		return fmt.Errorf("build kafka consumer: %w", err)
	}
	defer consumerClient.Close()

	consumer := kafkaadapter.NewConsumer(kafkaadapter.ConsumerConfig{
		Client:     consumerClient,
		Registerer: app.NewRegisterProfileUseCase(tx, clk, ids, metricsRecorder),
		Clock:      clk,
		Logger:     log,
		Propagator: providers.Propagator,
		Tracer:     providers.TracerProvider.Tracer(serviceName),
	})

	log.Info("starting", "grpc", cfg.GRPCAddr, "kafka", cfg.BrokerList())

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return grpcSrv.Run(groupCtx) })
	group.Go(func() error {
		relay.Run(groupCtx)
		return nil
	})
	group.Go(func() error { return consumer.Run(groupCtx) })

	if err := group.Wait(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	log.Info("stopped")
	return nil
}
