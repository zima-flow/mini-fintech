package interceptors

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestInterceptorOrder(t *testing.T) {
	t.Parallel()

	var order []string
	recorder := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(withOrderTrace(ctx, &order), req)
	}

	chain := append([]grpc.UnaryServerInterceptor{recorder}, Unary(testConfig(publicMethod))...)
	conn := serveProbe(t, chain, Stream(testConfig(publicMethod)), func(ctx context.Context) error {
		mark(ctx, "handler")
		return nil
	})

	require.NoError(t, conn.Invoke(context.Background(), publicMethod, new(emptypb.Empty), new(emptypb.Empty)))
	require.Equal(t, []string{"recovery", "requestid", "logging", "metrics", "auth", "handler"}, order)
}

func TestInterceptorOrder_Stream(t *testing.T) {
	t.Parallel()

	var order []string
	recorder := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, &contextStream{ServerStream: ss, ctx: withOrderTrace(ss.Context(), &order)})
	}

	chain := append([]grpc.StreamServerInterceptor{recorder}, Stream(testConfig(publicMethod, publicStream))...)
	conn := serveProbe(t, Unary(testConfig(publicMethod, publicStream)), chain, func(ctx context.Context) error {
		mark(ctx, "handler")
		return nil
	})

	stream, err := conn.NewStream(context.Background(), &grpc.StreamDesc{StreamName: "CallStream", ServerStreams: true}, publicStream)
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(new(emptypb.Empty)))
	require.NoError(t, stream.CloseSend())

	for {
		var msg emptypb.Empty
		if err := stream.RecvMsg(&msg); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.Equal(t, []string{"recovery", "requestid", "logging", "metrics", "auth", "handler"}, order)
}
