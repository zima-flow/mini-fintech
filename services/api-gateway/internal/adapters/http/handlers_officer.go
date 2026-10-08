package gatewayhttp

import (
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/common/v1"
	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
)

type OfficerHandlers struct {
	customer CustomerService
}

func NewOfficerHandlers(customer CustomerService) *OfficerHandlers {
	return &OfficerHandlers{customer: customer}
}

func RegisterOfficerRoutes(mux *http.ServeMux, h *OfficerHandlers, authenticate func(http.Handler) http.Handler) {
	staff := func(next http.Handler) http.Handler {
		return apply(authenticate, RequireRole(RoleOfficer, RoleAdmin)(next))
	}
	mux.Handle("GET /v1/officers/customers/{customer_id}", staff(http.HandlerFunc(h.GetCustomer)))
	mux.Handle("GET /v1/officers/customers", staff(http.HandlerFunc(h.ListCustomers)))
}

func (h *OfficerHandlers) GetCustomer(w http.ResponseWriter, r *http.Request) {
	customerID := r.PathValue("customer_id")
	if customerID == "" {
		writeError(w, r, status.Error(codes.InvalidArgument, "missing customer_id"))
		return
	}

	resp, err := h.customer.GetCustomer(r.Context(), &customerv1.GetCustomerRequest{CustomerId: customerID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *OfficerHandlers) ListCustomers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	req := &customerv1.ListCustomersRequest{}

	if raw := query.Get("status"); raw != "" {
		value, ok := customerv1.CustomerStatus_value[raw]
		if !ok {
			writeError(w, r, status.Error(codes.InvalidArgument, "invalid status filter"))
			return
		}
		req.Status = customerv1.CustomerStatus(value)
	}

	pagination := &commonv1.PaginationRequest{PageToken: query.Get("page_token")}
	if raw := query.Get("page_size"); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			writeError(w, r, status.Error(codes.InvalidArgument, "invalid page_size"))
			return
		}
		pagination.PageSize = int32(size)
	}
	req.Pagination = pagination

	resp, err := h.customer.ListCustomers(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}
