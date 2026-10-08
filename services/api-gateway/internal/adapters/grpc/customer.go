package grpcadapter

import (
	"context"

	"google.golang.org/grpc"

	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
)

type CustomerClient struct {
	customerv1.CustomerServiceClient
	conn *grpc.ClientConn
}

func NewCustomerClient(cfg Config) (*CustomerClient, error) {
	conn, err := newConn(cfg.CustomerAddr, cfg.Propagator)
	if err != nil {
		return nil, err
	}
	return &CustomerClient{CustomerServiceClient: customerv1.NewCustomerServiceClient(conn), conn: conn}, nil
}

func (c *CustomerClient) Close() error { return c.conn.Close() }

func (c *CustomerClient) Ready(ctx context.Context) error { return checkHealth(ctx, c.conn) }
