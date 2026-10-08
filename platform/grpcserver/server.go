package grpcserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/health"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const DefaultShutdownTimeout = 10 * time.Second

const (
	livenessService  = "liveness"
	readinessService = "readiness"
)

type Config struct {
	Address         string
	ShutdownTimeout time.Duration
	Register        func(grpc.ServiceRegistrar)
	Interceptors    interceptors.Config
	OnListen        func(addr net.Addr)
}

type Server struct {
	cfg       Config
	log       *slog.Logger
	readiness health.Reporter

	mu   sync.Mutex
	addr net.Addr
	grpc *grpc.Server
}

func New(cfg Config, logger *slog.Logger, reporter health.Reporter) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if reporter == nil {
		reporter = health.AlwaysReady{}
	}
	return &Server{cfg: cfg, log: logger, readiness: reporter}
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

func (s *Server) Run(ctx context.Context) error {
	if err := s.cfg.Interceptors.Auth.Validate(); err != nil {
		return fmt.Errorf("grpcserver: %w", err)
	}

	lis, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return fmt.Errorf("grpcserver: listen %q: %w", s.cfg.Address, err)
	}

	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptors.Unary(s.cfg.Interceptors)...),
		grpc.ChainStreamInterceptor(interceptors.Stream(s.cfg.Interceptors)...),
	)
	grpc_health_v1.RegisterHealthServer(grpcSrv, &healthServer{readiness: s.readiness})
	if s.cfg.Register != nil {
		s.cfg.Register(grpcSrv)
	}

	s.mu.Lock()
	s.addr = lis.Addr()
	s.grpc = grpcSrv
	s.mu.Unlock()

	if s.cfg.OnListen != nil {
		s.cfg.OnListen(lis.Addr())
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcSrv.Serve(lis) }()

	select {
	case <-ctx.Done():
		s.drain(grpcSrv)
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("grpcserver: serve: %w", err)
		}
		return nil
	}
}

func (s *Server) drain(grpcSrv *grpc.Server) {
	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}

	done := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		s.log.Warn("grpcserver: graceful drain timed out; forcing stop", slog.Duration("timeout", timeout))
		go grpcSrv.Stop()
	}
}

type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer
	readiness health.Reporter
}

func (h *healthServer) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	service := req.GetService()
	if service == "" || service == readinessService {
		if err := h.readiness.Readiness(ctx); err != nil {
			return healthResponse(grpc_health_v1.HealthCheckResponse_NOT_SERVING), nil
		}
		return healthResponse(grpc_health_v1.HealthCheckResponse_SERVING), nil
	}
	if service == livenessService {
		return healthResponse(grpc_health_v1.HealthCheckResponse_SERVING), nil
	}
	return nil, status.Errorf(codes.NotFound, "unknown health service %q", service)
}

func (h *healthServer) List(ctx context.Context, _ *grpc_health_v1.HealthListRequest) (*grpc_health_v1.HealthListResponse, error) {
	overall, err := h.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	return &grpc_health_v1.HealthListResponse{Statuses: map[string]*grpc_health_v1.HealthCheckResponse{
		"":               overall,
		livenessService:  healthResponse(grpc_health_v1.HealthCheckResponse_SERVING),
		readinessService: overall,
	}}, nil
}

func (h *healthServer) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	resp, err := h.Check(stream.Context(), req)
	if err != nil {
		return err
	}
	if err := stream.Send(resp); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func healthResponse(s grpc_health_v1.HealthCheckResponse_ServingStatus) *grpc_health_v1.HealthCheckResponse {
	return &grpc_health_v1.HealthCheckResponse{Status: s}
}
