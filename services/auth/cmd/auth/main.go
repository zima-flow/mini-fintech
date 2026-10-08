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

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
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
	authcrypto "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/crypto"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/grpc"
	authhttp "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/http"
	kafkaadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/kafka"
	metricsadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/metrics"
	authpostgres "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	tokenadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/token"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/config"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/migrations"
)

const (
	serviceName     = "auth"
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

	issuer, err := newIssuer(cfg)
	if err != nil {
		return err
	}

	verifier, err := authn.NewVerifier(authn.Config{
		JWKSURL:  cfg.AuthJWKSURL,
		Issuer:   cfg.AuthIssuer,
		Audience: cfg.AuthAudience,
		Dev:      cfg.AuthDev,
		Clock:    clock.Real{},
	})
	if err != nil {
		return fmt.Errorf("build verifier: %w", err)
	}

	clk := clock.Real{}
	ids := id.UUIDv7{}
	tx := authpostgres.NewTransactor(pool)
	hasher := authcrypto.NewHasher()
	ttls := app.TokenTTLs{Access: cfg.AuthAccessTTL, Refresh: cfg.AuthRefreshTTL}
	metricsRecorder := metricsadapter.NewRecorder(providers.MeterProvider.Meter(serviceName))

	authSrv := grpcadapter.NewServer(grpcadapter.UseCases{
		Register:      app.NewRegisterUseCase(tx, hasher, clk, ids, metricsRecorder),
		Login:         app.NewLoginUseCase(tx, hasher, issuer, clk, ids, ttls),
		Refresh:       app.NewRefreshUseCase(tx, issuer, clk, ids, ttls),
		Logout:        app.NewLogoutUseCase(tx, clk),
		ValidateToken: app.NewValidateTokenUseCase(verifier),
		CreateOfficer: app.NewCreateOfficerUseCase(tx, hasher, clk, ids),
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
				PublicMethods: append(grpcadapter.PublicMethods(),
					reflectionV1Method,
					reflectionV1AlphaMethod,
				),
			},
		},
		Register: func(reg grpc.ServiceRegistrar) {
			grpcadapter.Register(reg, authSrv)
			if server, ok := reg.(reflection.GRPCServer); ok {
				reflection.Register(server)
			}
		},
		OnListen: func(addr net.Addr) {
			log.Info("listening", "transport", "grpc", "addr", addr.String())
		},
	}, log, readiness)

	jwksSrv := authhttp.NewServer(authhttp.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         authhttp.Handler(issuer),
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          log,
	})

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
		Topics:   []string{events.TopicCustomer},
	})
	if err != nil {
		return fmt.Errorf("build kafka consumer: %w", err)
	}
	defer consumerClient.Close()

	consumer := kafkaadapter.NewConsumer(kafkaadapter.ConsumerConfig{
		Client:     consumerClient,
		Linker:     app.NewLinkCustomerUseCase(tx, clk),
		Clock:      clk,
		Logger:     log,
		Propagator: providers.Propagator,
		Tracer:     providers.TracerProvider.Tracer(serviceName),
	})

	log.Info("starting", "grpc", cfg.GRPCAddr, "http", cfg.HTTPAddr, "kafka", cfg.BrokerList())

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return grpcSrv.Run(groupCtx) })
	group.Go(func() error { return jwksSrv.Run(groupCtx) })
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

func newIssuer(cfg config.Config) (*tokenadapter.Issuer, error) {
	published := make([]tokenadapter.PublishedKey, 0, len(cfg.PublishedKeyPaths()))
	for _, path := range cfg.PublishedKeyPaths() {
		key, err := tokenadapter.LoadPublicKey(path)
		if err != nil {
			return nil, fmt.Errorf("load published key %s: %w", path, err)
		}
		published = append(published, tokenadapter.PublishedKey{Public: key})
	}

	issuer, err := tokenadapter.New(tokenadapter.Config{
		KeyPath:       cfg.AuthJWTKeyPath,
		KeyID:         cfg.AuthJWTKeyID,
		Issuer:        cfg.AuthIssuer,
		Audience:      cfg.AuthAudience,
		PublishedKeys: published,
	})
	if err != nil {
		return nil, fmt.Errorf("build token issuer: %w", err)
	}
	return issuer, nil
}
