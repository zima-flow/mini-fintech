package interceptors

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

const (
	probeService    = "probe.Probe"
	publicMethod    = "/probe.Probe/Call"
	protectedMethod = "/probe.Probe/Protected"
	publicStream    = "/probe.Probe/CallStream"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(public ...string) Config {
	return Config{
		Logger:    testLogger(),
		RequestID: id.UUIDv7{},
		Auth:      AuthConfig{PublicMethods: public},
	}
}

type probeHandlerFunc func(ctx context.Context) error

func serveProbe(
	t *testing.T,
	unary []grpc.UnaryServerInterceptor,
	stream []grpc.StreamServerInterceptor,
	onCall probeHandlerFunc,
) *grpc.ClientConn {
	t.Helper()

	probeMethod := func(method string) grpc.MethodDesc {
		return grpc.MethodDesc{
			MethodName: method,
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(emptypb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, _ any) (any, error) {
					if onCall != nil {
						if err := onCall(ctx); err != nil {
							return nil, err
						}
					}
					return new(emptypb.Empty), nil
				}
				if interceptor == nil {
					return handler(ctx, in)
				}
				fullMethod := "/" + probeService + "/" + method
				return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: fullMethod}, handler)
			},
		}
	}

	desc := grpc.ServiceDesc{
		ServiceName: probeService,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			probeMethod("Call"),
			probeMethod("Protected"),
		},
		Streams: []grpc.StreamDesc{{
			StreamName:    "CallStream",
			ServerStreams: true,
			Handler: func(srv any, ss grpc.ServerStream) error {
				if onCall != nil {
					if err := onCall(ss.Context()); err != nil {
						return err
					}
				}
				return ss.SendMsg(new(emptypb.Empty))
			},
		}},
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)
	srv.RegisterService(&desc, struct{}{})

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
	return conn
}
