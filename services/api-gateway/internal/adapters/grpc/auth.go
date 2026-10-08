package grpcadapter

import (
	"context"

	"google.golang.org/grpc"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
)

type AuthClient struct {
	authv1.AuthServiceClient
	conn *grpc.ClientConn
}

func NewAuthClient(cfg Config) (*AuthClient, error) {
	conn, err := newConn(cfg.AuthAddr, cfg.Propagator)
	if err != nil {
		return nil, err
	}
	return &AuthClient{AuthServiceClient: authv1.NewAuthServiceClient(conn), conn: conn}, nil
}

func (c *AuthClient) Close() error { return c.conn.Close() }

func (c *AuthClient) Ready(ctx context.Context) error { return checkHealth(ctx, c.conn) }
