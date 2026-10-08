package gatewayhttp

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

type CustomerService interface {
	GetProfile(ctx context.Context, in *customerv1.GetProfileRequest, opts ...grpc.CallOption) (*customerv1.GetProfileResponse, error)
	UpdateProfile(ctx context.Context, in *customerv1.UpdateProfileRequest, opts ...grpc.CallOption) (*customerv1.UpdateProfileResponse, error)
	GetCustomer(ctx context.Context, in *customerv1.GetCustomerRequest, opts ...grpc.CallOption) (*customerv1.GetCustomerResponse, error)
	GetCustomerStatus(ctx context.Context, in *customerv1.GetCustomerStatusRequest, opts ...grpc.CallOption) (*customerv1.GetCustomerStatusResponse, error)
	ListCustomers(ctx context.Context, in *customerv1.ListCustomersRequest, opts ...grpc.CallOption) (*customerv1.ListCustomersResponse, error)
}

type CustomerHandlers struct {
	customer CustomerService
}

func NewCustomerHandlers(customer CustomerService) *CustomerHandlers {
	return &CustomerHandlers{customer: customer}
}

func RegisterCustomerRoutes(mux *http.ServeMux, h *CustomerHandlers, authenticate func(http.Handler) http.Handler) {
	owner := func(next http.Handler) http.Handler {
		return apply(authenticate, RequireRole(RoleClient)(next))
	}
	mux.Handle("GET /v1/customer/profile", owner(http.HandlerFunc(h.GetProfile)))
	mux.Handle("PUT /v1/customer/profile", owner(http.HandlerFunc(h.UpdateProfile)))
	mux.Handle("GET /v1/customer/status", owner(http.HandlerFunc(h.GetStatus)))
}

func (h *CustomerHandlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	resp, err := h.customer.GetProfile(r.Context(), &customerv1.GetProfileRequest{})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *CustomerHandlers) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req customerv1.UpdateProfileRequest
	if err := decodeProto(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	key, ok := idempotencyKey(r)
	if !ok {
		writeError(w, r, status.Error(codes.InvalidArgument, "missing Idempotency-Key header"))
		return
	}
	req.IdempotencyKey = key

	resp, err := h.customer.UpdateProfile(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *CustomerHandlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	req := &customerv1.GetCustomerStatusRequest{}
	if principal, ok := interceptors.PrincipalFromContext(r.Context()); ok {
		req.CustomerId = principal.CustomerID
	}

	resp, err := h.customer.GetCustomerStatus(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}
