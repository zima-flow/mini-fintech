package grpcadapter

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/common/v1"
	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	platformotel "github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type (
	ProfileReader interface {
		GetProfile(context.Context, app.GetProfileCommand) (app.GetProfileResult, error)
	}
	ProfileUpdater interface {
		UpdateProfile(context.Context, app.UpdateProfileCommand) (app.UpdateProfileResult, error)
	}
	CustomerReader interface {
		GetCustomer(context.Context, app.GetCustomerCommand) (app.GetCustomerResult, error)
	}
	StatusReader interface {
		GetCustomerStatus(context.Context, app.GetCustomerStatusCommand) (app.GetCustomerStatusResult, error)
	}
	CustomerLister interface {
		ListCustomers(context.Context, app.ListCustomersCommand) (app.ListCustomersResult, error)
	}
)

type UseCases struct {
	GetProfile        ProfileReader
	UpdateProfile     ProfileUpdater
	GetCustomer       CustomerReader
	GetCustomerStatus StatusReader
	ListCustomers     CustomerLister
}

type Server struct {
	customerv1.UnimplementedCustomerServiceServer
	uc         UseCases
	propagator propagation.TextMapPropagator
}

func NewServer(uc UseCases, propagator propagation.TextMapPropagator) *Server {
	return &Server{uc: uc, propagator: propagator}
}

func Register(reg grpc.ServiceRegistrar, srv *Server) {
	customerv1.RegisterCustomerServiceServer(reg, srv)
}

func (s *Server) GetProfile(ctx context.Context, _ *customerv1.GetProfileRequest) (*customerv1.GetProfileResponse, error) {
	userID, err := actorUserID(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.GetProfile.GetProfile(ctx, app.GetProfileCommand{UserID: userID})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &customerv1.GetProfileResponse{Customer: customerToProto(result.Customer)}, nil
}

func (s *Server) UpdateProfile(ctx context.Context, req *customerv1.UpdateProfileRequest) (*customerv1.UpdateProfileResponse, error) {
	userID, err := actorUserID(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.UpdateProfile.UpdateProfile(ctx, app.UpdateProfileCommand{
		UserID:         userID,
		FullName:       req.GetFullName(),
		DateOfBirth:    req.GetDateOfBirth(),
		Address:        req.GetAddress(),
		Phone:          req.GetPhone(),
		Citizenship:    req.GetCitizenship(),
		IdempotencyKey: req.GetIdempotencyKey(),
		Headers:        platformotel.InjectHeaders(ctx, s.propagator),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &customerv1.UpdateProfileResponse{Customer: customerToProto(result.Customer)}, nil
}

func (s *Server) GetCustomer(ctx context.Context, req *customerv1.GetCustomerRequest) (*customerv1.GetCustomerResponse, error) {
	role, err := actorRole(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.GetCustomer.GetCustomer(ctx, app.GetCustomerCommand{
		CustomerID: req.GetCustomerId(),
		ActorRole:  role,
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &customerv1.GetCustomerResponse{Customer: customerToProto(result.Customer)}, nil
}

func (s *Server) GetCustomerStatus(ctx context.Context, req *customerv1.GetCustomerStatusRequest) (*customerv1.GetCustomerStatusResponse, error) {
	role, err := actorRole(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := actorUserID(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.GetCustomerStatus.GetCustomerStatus(ctx, app.GetCustomerStatusCommand{
		UserID:     userID,
		CustomerID: req.GetCustomerId(),
		ActorRole:  role,
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &customerv1.GetCustomerStatusResponse{
		CustomerId: result.CustomerID,
		Status:     statusToProto(result.Status),
	}, nil
}

func (s *Server) ListCustomers(ctx context.Context, req *customerv1.ListCustomersRequest) (*customerv1.ListCustomersResponse, error) {
	role, err := actorRole(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.uc.ListCustomers.ListCustomers(ctx, app.ListCustomersCommand{
		ActorRole: role,
		Status:    statusFromProto(req.GetStatus()),
		PageSize:  int(req.GetPagination().GetPageSize()),
		PageToken: req.GetPagination().GetPageToken(),
	})
	if err != nil {
		return nil, errs.ToStatus(err).Err()
	}
	return &customerv1.ListCustomersResponse{
		Customers:  customersToProto(result.Customers),
		Pagination: &commonv1.PaginationResponse{NextPageToken: result.NextPageToken},
	}, nil
}

func actorUserID(ctx context.Context) (string, error) {
	principal, ok := interceptors.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return "", status.Error(codes.Unauthenticated, "invalid access token")
	}
	return principal.Subject, nil
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

func customerToProto(c domain.Customer) *customerv1.Customer {
	return &customerv1.Customer{
		CustomerId:  c.ID,
		UserId:      c.UserID,
		FullName:    c.FullName,
		DateOfBirth: c.DateOfBirth,
		Address:     c.Address,
		Phone:       c.Phone,
		Citizenship: c.Citizenship,
		Status:      statusToProto(c.Status),
		CreatedAt:   timestamppb.New(c.CreatedAt.UTC()),
		UpdatedAt:   timestamppb.New(c.UpdatedAt.UTC()),
	}
}

func customersToProto(customers []domain.Customer) []*customerv1.Customer {
	out := make([]*customerv1.Customer, len(customers))
	for i, c := range customers {
		out[i] = customerToProto(c)
	}
	return out
}

func statusToProto(s domain.CustomerStatus) customerv1.CustomerStatus {
	switch s {
	case domain.StatusNew:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_NEW
	case domain.StatusProfileFilled:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED
	case domain.StatusOnKYC:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_ON_KYC
	case domain.StatusActive:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE
	case domain.StatusRejected:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_REJECTED
	case domain.StatusBlocked:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_BLOCKED
	default:
		return customerv1.CustomerStatus_CUSTOMER_STATUS_UNSPECIFIED
	}
}

func statusFromProto(s customerv1.CustomerStatus) domain.CustomerStatus {
	switch s {
	case customerv1.CustomerStatus_CUSTOMER_STATUS_NEW:
		return domain.StatusNew
	case customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED:
		return domain.StatusProfileFilled
	case customerv1.CustomerStatus_CUSTOMER_STATUS_ON_KYC:
		return domain.StatusOnKYC
	case customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE:
		return domain.StatusActive
	case customerv1.CustomerStatus_CUSTOMER_STATUS_REJECTED:
		return domain.StatusRejected
	case customerv1.CustomerStatus_CUSTOMER_STATUS_BLOCKED:
		return domain.StatusBlocked
	default:
		return ""
	}
}
