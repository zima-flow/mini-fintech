package grpcadapter

import (
	"context"

	"google.golang.org/grpc"

	examplev1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/example/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/domain"
)

type UseCase interface {
	Echo(ctx context.Context, message string) (domain.Echo, error)
}

type Server struct {
	examplev1.UnimplementedExampleServiceServer
	useCase UseCase
}

func NewServer(useCase UseCase) *Server {
	return &Server{useCase: useCase}
}

func Register(reg grpc.ServiceRegistrar, srv *Server) {
	examplev1.RegisterExampleServiceServer(reg, srv)
}

func (s *Server) Echo(ctx context.Context, req *examplev1.EchoRequest) (*examplev1.EchoResponse, error) {
	echo, err := s.useCase.Echo(ctx, req.GetMessage())
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &examplev1.EchoResponse{Message: echo.Message}, nil
}

func (s *Server) ProtectedEcho(ctx context.Context, req *examplev1.ProtectedEchoRequest) (*examplev1.ProtectedEchoResponse, error) {
	echo, err := s.useCase.Echo(ctx, req.GetMessage())
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &examplev1.ProtectedEchoResponse{Message: echo.Message}, nil
}
