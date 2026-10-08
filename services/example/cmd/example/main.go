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

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	examplev1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/example/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/grpcserver"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/health"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/logger"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/adapters/grpc"
	echopostgres "github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/config"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/migrations"
)

const (
	serviceName     = "example"
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

	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.DatabaseURL})
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

	providers, err := otel.Setup(ctx, otel.Config{
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

	repo := echopostgres.NewEchoRepo(postgres.New(pool), id.UUIDv7{})
	useCase := app.NewUseCase(repo, clock.Real{})

	readiness := health.New(health.Probe{Name: "postgres", Check: pool.Ping})

	srv := grpcserver.New(grpcserver.Config{
		Address:         cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Interceptors: interceptors.Config{
			Logger:     log,
			Meter:      providers.MeterProvider.Meter(serviceName),
			Tracer:     providers.TracerProvider.Tracer(serviceName),
			Propagator: providers.Propagator,
			RequestID:  id.UUIDv7{},
			Auth: interceptors.AuthConfig{
				JWKSURL:  cfg.AuthJWKSURL,
				Issuer:   cfg.AuthIssuer,
				Audience: cfg.AuthAudience,
				Dev:      cfg.AuthDev,
				PublicMethods: []string{
					examplev1.ExampleService_Echo_FullMethodName,
					reflectionV1Method,
					reflectionV1AlphaMethod,
				},
			},
		},
		Register: func(reg grpc.ServiceRegistrar) {
			grpcadapter.Register(reg, grpcadapter.NewServer(useCase))
			if server, ok := reg.(reflection.GRPCServer); ok {
				reflection.Register(server)
			}
		},
		OnListen: func(addr net.Addr) {
			log.Info("listening", "addr", addr.String())
		},
	}, log, readiness)

	log.Info("starting", "addr", cfg.GRPCAddr)
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	log.Info("stopped")
	return nil
}
