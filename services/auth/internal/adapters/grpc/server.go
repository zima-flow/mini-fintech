package grpcadapter

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	platformotel "github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type (
	Registerer interface {
		Register(context.Context, app.RegisterCommand) (app.RegisterResult, error)
	}
	Loginer interface {
		Login(context.Context, app.LoginCommand) (app.LoginResult, error)
	}
	Refresher interface {
		Refresh(context.Context, app.RefreshCommand) (app.RefreshResult, error)
	}
	Logouter interface {
		Logout(context.Context, app.LogoutCommand) error
	}
	TokenValidator interface {
		ValidateToken(context.Context, app.ValidateTokenCommand) (app.ValidateTokenResult, error)
	}
	OfficerCreator interface {
		CreateOfficer(context.Context, app.CreateOfficerCommand) (app.CreateOfficerResult, error)
	}
)

type UseCases struct {
	Register      Registerer
	Login         Loginer
	Refresh       Refresher
	Logout        Logouter
	ValidateToken TokenValidator
	CreateOfficer OfficerCreator
}

type Server struct {
	authv1.UnimplementedAuthServiceServer
	uc         UseCases
	propagator propagation.TextMapPropagator
}

func NewServer(uc UseCases, propagator propagation.TextMapPropagator) *Server {
	return &Server{uc: uc, propagator: propagator}
}

func Register(reg grpc.ServiceRegistrar, srv *Server) {
	authv1.RegisterAuthServiceServer(reg, srv)
}

func PublicMethods() []string {
	return []string{
		authv1.AuthService_Register_FullMethodName,
		authv1.AuthService_Login_FullMethodName,
		authv1.AuthService_Refresh_FullMethodName,
		authv1.AuthService_Logout_FullMethodName,
	}
}

func (s *Server) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	result, err := s.uc.Register.Register(ctx, app.RegisterCommand{
		Email:          req.GetEmail(),
		Password:       req.GetPassword(),
		IdempotencyKey: req.GetIdempotencyKey(),
		Headers:        platformotel.InjectHeaders(ctx, s.propagator),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.RegisterResponse{
		UserId:     result.UserID,
		CustomerId: result.CustomerID,
		Role:       roleToProto(result.Role),
	}, nil
}

func (s *Server) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	result, err := s.uc.Login.Login(ctx, app.LoginCommand{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.LoginResponse{
		Tokens:     tokenPairToProto(result.Tokens),
		Role:       roleToProto(result.Role),
		UserId:     result.UserID,
		CustomerId: result.CustomerID,
	}, nil
}

func (s *Server) Refresh(ctx context.Context, req *authv1.RefreshRequest) (*authv1.RefreshResponse, error) {
	result, err := s.uc.Refresh.Refresh(ctx, app.RefreshCommand{
		RefreshToken: req.GetRefreshToken(),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.RefreshResponse{Tokens: tokenPairToProto(result.Tokens)}, nil
}

func (s *Server) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if err := s.uc.Logout.Logout(ctx, app.LogoutCommand{
		RefreshToken: req.GetRefreshToken(),
	}); err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.LogoutResponse{}, nil
}

func (s *Server) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	result, err := s.uc.ValidateToken.ValidateToken(ctx, app.ValidateTokenCommand{
		AccessToken: req.GetAccessToken(),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.ValidateTokenResponse{
		Valid:      true,
		UserId:     result.UserID,
		Role:       roleToProto(result.Role),
		CustomerId: result.CustomerID,
		ExpiresAt:  timestamppb.New(result.ExpiresAt),
	}, nil
}

func (s *Server) CreateOfficer(ctx context.Context, req *authv1.CreateOfficerRequest) (*authv1.CreateOfficerResponse, error) {
	actor, err := actorRole(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.CreateOfficer.CreateOfficer(ctx, app.CreateOfficerCommand{
		Email:          req.GetEmail(),
		Password:       req.GetPassword(),
		IdempotencyKey: req.GetIdempotencyKey(),
		ActorRole:      actor,
		Headers:        platformotel.InjectHeaders(ctx, s.propagator),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &authv1.CreateOfficerResponse{
		UserId: result.UserID,
		Role:   roleToProto(result.Role),
	}, nil
}

func actorRole(ctx context.Context) (domain.Role, error) {
	principal, ok := interceptors.PrincipalFromContext(ctx)
	if !ok || len(principal.Roles) != 1 {
		return "", status.Error(codes.Unauthenticated, "invalid access token")
	}
	role := domain.Role(principal.Roles[0])
	if !role.Valid() {
		return "", status.Error(codes.Unauthenticated, "invalid access token")
	}
	return role, nil
}

func roleToProto(role domain.Role) authv1.Role {
	switch role {
	case domain.RoleClient:
		return authv1.Role_ROLE_CLIENT
	case domain.RoleOfficer:
		return authv1.Role_ROLE_OFFICER
	case domain.RoleAdmin:
		return authv1.Role_ROLE_ADMIN
	default:
		return authv1.Role_ROLE_UNSPECIFIED
	}
}

func tokenPairToProto(pair domain.TokenPair) *authv1.TokenPair {
	return &authv1.TokenPair{
		AccessToken:          pair.AccessToken,
		RefreshToken:         pair.RefreshToken,
		AccessTokenExpiresAt: timestamppb.New(pair.AccessExpiresAt),
	}
}
