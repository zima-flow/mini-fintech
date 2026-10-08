package grpcadapter_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	examplev1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/example/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/adapters/grpc"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/domain"
)

type fakeUseCase struct{}

func (fakeUseCase) Echo(_ context.Context, message string) (domain.Echo, error) {
	return domain.Echo{Message: message}, nil
}

func startServer(t *testing.T) examplev1.ExampleServiceClient {
	t.Helper()

	auth := interceptors.AuthConfig{
		PublicMethods: []string{examplev1.ExampleService_Echo_FullMethodName},
		Dev:           true,
	}
	cfg := interceptors.Config{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestID: id.UUIDv7{},
		Auth:      auth,
	}
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptors.Unary(cfg)...),
		grpc.ChainStreamInterceptor(interceptors.Stream(cfg)...),
	)
	grpcadapter.Register(srv, grpcadapter.NewServer(fakeUseCase{}))

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return examplev1.NewExampleServiceClient(conn)
}

func TestEcho_ReturnsMessage(t *testing.T) {
	t.Parallel()

	resp, err := startServer(t).Echo(context.Background(), &examplev1.EchoRequest{Message: "hello"})
	require.NoError(t, err)
	require.Equal(t, "hello", resp.GetMessage())
}

func TestProtectedEcho_WithoutToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	_, err := startServer(t).ProtectedEcho(context.Background(), &examplev1.ProtectedEchoRequest{Message: "secret"})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
