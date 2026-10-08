package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/health"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/logger"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	platformredis "github.com/zima-flow/go-mentor/mini-fintech/platform/redis"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/grpc"
	gatewayhttp "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/http"
	redisadapter "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/redis"
	"github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/config"
)

const (
	serviceName     = "api-gateway"
	otelShutdownTTL = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate", false, "accepted for the workspace `make migrate` target; the gateway has no database")
	flag.Parse()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, serviceName)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if *migrateOnly {
		log.Info("api-gateway has no database; nothing to migrate")
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

	authClient, err := grpcadapter.NewAuthClient(grpcadapter.Config{
		AuthAddr:   cfg.AuthGRPCAddr,
		Propagator: providers.Propagator,
	})
	if err != nil {
		return fmt.Errorf("build auth client: %w", err)
	}
	defer func() { _ = authClient.Close() }()

	customerClient, err := grpcadapter.NewCustomerClient(grpcadapter.Config{
		CustomerAddr: cfg.CustomerGRPCAddr,
		Propagator:   providers.Propagator,
	})
	if err != nil {
		return fmt.Errorf("build customer client: %w", err)
	}
	defer func() { _ = customerClient.Close() }()

	redisClient := platformredis.NewClient(platformredis.Config{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer func() { _ = redisClient.Close() }()

	limiter := redisadapter.NewLimiter(
		redisadapter.NewRedisRunner(redisClient),
		log,
		providers.MeterProvider.Meter(serviceName),
	)

	readiness := health.New(
		health.Probe{Name: "auth", Check: authClient.Ready},
		health.Probe{Name: "customer", Check: customerClient.Ready},
		health.Probe{Name: "redis", Check: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		}},
	)

	srv := gatewayhttp.NewServer(gatewayhttp.ServerConfig{
		Addr: cfg.HTTPAddr,
		Handler: gatewayhttp.NewRouter(
			gatewayhttp.Config{
				Logger:     log,
				Tracer:     providers.TracerProvider.Tracer(serviceName),
				Propagator: providers.Propagator,
				RequestID:  id.UUIDv7{},
				Meter:      providers.MeterProvider.Meter(serviceName),
			},
			newRouteTable(cfg, log, verifier, authClient, customerClient, limiter, readiness),
		),
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          log,
	})

	log.Info("starting",
		"http", cfg.HTTPAddr,
		"auth", cfg.AuthGRPCAddr,
		"customer", cfg.CustomerGRPCAddr,
		"redis", cfg.RedisAddr,
		"jwks", cfg.AuthJWKSURL,
	)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	log.Info("stopped")
	return nil
}

func newRouteTable(
	cfg config.Config,
	log *slog.Logger,
	verifier gatewayhttp.TokenVerifier,
	authSvc *grpcadapter.AuthClient,
	customerSvc *grpcadapter.CustomerClient,
	limiter gatewayhttp.RateLimiter,
	readiness health.Reporter,
) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", liveness)
	mux.HandleFunc("GET /readyz", readinessHandler(log, readiness))

	public := http.NewServeMux()
	gatewayhttp.RegisterAuthRoutes(public, gatewayhttp.NewAuthHandlers(authSvc))
	mux.Handle("/v1/auth/", gatewayhttp.RateLimit(limiter, redisadapter.Policy{
		Name:  "ip-auth",
		Rate:  cfg.IPRefillRate(),
		Burst: cfg.RateLimitIPBurst,
	}, gatewayhttp.IPKey)(public))

	authenticate := gatewayhttp.RequireAuth(verifier)
	userLimit := gatewayhttp.RateLimit(limiter, redisadapter.Policy{
		Name:  "user",
		Rate:  cfg.UserRefillRate(),
		Burst: cfg.RateLimitUserBurst,
	}, gatewayhttp.UserKey)
	authChain := func(next http.Handler) http.Handler {
		return authenticate(userLimit(next))
	}

	gatewayhttp.RegisterCustomerRoutes(mux, gatewayhttp.NewCustomerHandlers(customerSvc), authChain)
	gatewayhttp.RegisterOfficerRoutes(mux, gatewayhttp.NewOfficerHandlers(customerSvc), authChain)
	gatewayhttp.RegisterAdminRoutes(mux, gatewayhttp.NewAdminHandlers(authSvc), authChain)

	return gatewayhttp.CORS(gatewayhttp.CORSConfig{AllowedOrigins: cfg.CORSOrigins()})(mux)
}

func liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func readinessHandler(log *slog.Logger, reporter health.Reporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reporter != nil {
			if err := reporter.Readiness(r.Context()); err != nil {
				log.WarnContext(r.Context(), "readiness check failed", slog.String("error", err.Error()))
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
