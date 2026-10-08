package grpcadapter

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/grpcclient"
)

type Config struct {
	AuthAddr     string
	CustomerAddr string
	Propagator   propagation.TextMapPropagator
}

func newConn(target string, propagator propagation.TextMapPropagator, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	if target == "" {
		return nil, fmt.Errorf("grpcadapter: empty target: %w", errs.ErrInvalidArgument)
	}

	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.Unary(grpcclient.Config{Propagator: propagator})),
		grpc.WithChainStreamInterceptor(grpcclient.Stream(grpcclient.Config{Propagator: propagator})),
	}, extra...)

	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpcadapter: dial %q: %w", target, err)
	}
	return conn, nil
}

func checkHealth(ctx context.Context, conn *grpc.ClientConn) error {
	if conn == nil {
		return fmt.Errorf("grpcadapter: no connection: %w", errs.ErrUnavailable)
	}
	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpcadapter: health check: %w", errors.Join(errs.ErrUnavailable, err))
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpcadapter: upstream status %s: %w", resp.GetStatus(), errs.ErrUnavailable)
	}
	return nil
}
