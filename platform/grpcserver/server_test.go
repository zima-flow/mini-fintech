package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const slowMethod = "/probe.Slow/Call"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGracefulShutdown(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	conn, cancel, runErr := startSlowServer(t, 2*time.Second, func() {
		once.Do(func() { close(started) })
		<-release
	})

	rpcErr := make(chan error, 1)
	go func() {
		rpcErr <- conn.Invoke(context.Background(), slowMethod, new(emptypb.Empty), new(emptypb.Empty))
	}()

	waitSignal(t, started, 5*time.Second, "handler did not start")

	cancel()
	close(release)

	select {
	case err := <-rpcErr:
		require.NoError(t, err, "in-flight RPC should complete with OK")
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight RPC did not complete")
	}

	select {
	case err := <-runErr:
		require.NoError(t, err, "Run should return nil after a graceful drain")
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within the bound")
	}
}

func TestGracefulShutdown_TimeoutForcesStop(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { close(release) })

	conn, cancel, runErr := startSlowServer(t, 200*time.Millisecond, func() {
		once.Do(func() { close(started) })
		<-release
	})

	go func() { _ = conn.Invoke(context.Background(), slowMethod, new(emptypb.Empty), new(emptypb.Empty)) }()

	waitSignal(t, started, 5*time.Second, "handler did not start")
	cancel()

	select {
	case err := <-runErr:
		require.NoError(t, err, "Run should return after a forced stop")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not force-stop within the bound")
	}
}

func TestRunRejectsMisconfiguredAuth(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := Config{
		Address: "127.0.0.1:0",
		Interceptors: interceptors.Config{
			Logger: testLogger(),
			Auth:   interceptors.AuthConfig{},
		},
	}
	err := New(cfg, testLogger(), nil).Run(ctx)
	require.Error(t, err, "a misconfigured service must fail at startup")
	require.Contains(t, err.Error(), "Issuer and Audience")
}

func startSlowServer(t *testing.T, shutdownTimeout time.Duration, onCall func()) (*grpc.ClientConn, context.CancelFunc, <-chan error) {
	t.Helper()

	addrCh := make(chan string, 1)
	cfg := Config{
		Address:         "127.0.0.1:0",
		ShutdownTimeout: shutdownTimeout,
		Interceptors: interceptors.Config{
			Logger:    testLogger(),
			RequestID: id.UUIDv7{},
			Auth:      interceptors.AuthConfig{Dev: true, PublicMethods: []string{slowMethod}},
		},
		Register: func(reg grpc.ServiceRegistrar) {
			reg.RegisterService(slowDesc(onCall), struct{}{})
		},
		OnListen: func(addr net.Addr) { addrCh <- addr.String() },
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- New(cfg, testLogger(), nil).Run(ctx) }()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("server did not listen")
	}

	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		cancel()
	})
	return conn, cancel, runErr
}

func slowDesc(onCall func()) *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "probe.Slow",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Call",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(emptypb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, _ any) (any, error) {
					onCall()
					return new(emptypb.Empty), nil
				}
				if interceptor == nil {
					return handler(ctx, in)
				}
				return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: slowMethod}, handler)
			},
		}},
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal(msg)
	}
}
