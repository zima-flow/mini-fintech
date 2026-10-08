package authhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const DefaultShutdownTimeout = 10 * time.Second

const defaultReadHeaderTimeout = 5 * time.Second

type ServerConfig struct {
	Addr              string
	Handler           http.Handler
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	Logger            *slog.Logger
}

type Server struct {
	cfg ServerConfig
	log *slog.Logger

	mu   sync.Mutex
	addr net.Addr
}

func NewServer(cfg ServerConfig) *Server {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{cfg: cfg, log: log}
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

func (s *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("authhttp: listen %q: %w", s.cfg.Addr, err)
	}

	readHeader := s.cfg.ReadHeaderTimeout
	if readHeader <= 0 {
		readHeader = defaultReadHeaderTimeout
	}
	srv := &http.Server{Handler: s.cfg.Handler, ReadHeaderTimeout: readHeader}

	s.mu.Lock()
	s.addr = lis.Addr()
	s.mu.Unlock()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	select {
	case <-ctx.Done():
		s.drain(srv)
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("authhttp: serve: %w", err)
	}
}

func (s *Server) drain(srv *http.Server) {
	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("authhttp: graceful shutdown timed out; forcing close",
			slog.Duration("timeout", timeout),
			slog.String("error", err.Error()),
		)
		_ = srv.Close()
	}
}
