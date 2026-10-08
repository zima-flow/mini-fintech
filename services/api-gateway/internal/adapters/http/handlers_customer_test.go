package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

type fakeCustomer struct {
	getProfileResp  *customerv1.GetProfileResponse
	getProfileErr   error
	updateResp      *customerv1.UpdateProfileResponse
	updateErr       error
	getCustomerResp *customerv1.GetCustomerResponse
	getCustomerErr  error
	getStatusResp   *customerv1.GetCustomerStatusResponse
	getStatusErr    error
	listResp        *customerv1.ListCustomersResponse
	listErr         error

	lastGetProfile  *customerv1.GetProfileRequest
	lastUpdate      *customerv1.UpdateProfileRequest
	lastGetCustomer *customerv1.GetCustomerRequest
	lastGetStatus   *customerv1.GetCustomerStatusRequest
	lastList        *customerv1.ListCustomersRequest
}

func (f *fakeCustomer) GetProfile(_ context.Context, in *customerv1.GetProfileRequest, _ ...grpc.CallOption) (*customerv1.GetProfileResponse, error) {
	f.lastGetProfile = in
	if f.getProfileResp == nil {
		return &customerv1.GetProfileResponse{}, f.getProfileErr
	}
	return f.getProfileResp, f.getProfileErr
}

func (f *fakeCustomer) UpdateProfile(_ context.Context, in *customerv1.UpdateProfileRequest, _ ...grpc.CallOption) (*customerv1.UpdateProfileResponse, error) {
	f.lastUpdate = in
	if f.updateResp == nil {
		return &customerv1.UpdateProfileResponse{}, f.updateErr
	}
	return f.updateResp, f.updateErr
}

func (f *fakeCustomer) GetCustomer(_ context.Context, in *customerv1.GetCustomerRequest, _ ...grpc.CallOption) (*customerv1.GetCustomerResponse, error) {
	f.lastGetCustomer = in
	if f.getCustomerResp == nil {
		return &customerv1.GetCustomerResponse{}, f.getCustomerErr
	}
	return f.getCustomerResp, f.getCustomerErr
}

func (f *fakeCustomer) GetCustomerStatus(_ context.Context, in *customerv1.GetCustomerStatusRequest, _ ...grpc.CallOption) (*customerv1.GetCustomerStatusResponse, error) {
	f.lastGetStatus = in
	if f.getStatusResp == nil {
		return &customerv1.GetCustomerStatusResponse{}, f.getStatusErr
	}
	return f.getStatusResp, f.getStatusErr
}

func (f *fakeCustomer) ListCustomers(_ context.Context, in *customerv1.ListCustomersRequest, _ ...grpc.CallOption) (*customerv1.ListCustomersResponse, error) {
	f.lastList = in
	if f.listResp == nil {
		return &customerv1.ListCustomersResponse{}, f.listErr
	}
	return f.listResp, f.listErr
}

func TestCustomerHandlers_GetProfile_Success(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getProfileResp: &customerv1.GetProfileResponse{Customer: &customerv1.Customer{
		CustomerId: "cust-1", UserId: "user-1", Status: customerv1.CustomerStatus_CUSTOMER_STATUS_NEW,
	}}}
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).GetProfile(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, customer.lastGetProfile)
	body := decodeBody(t, rec)
	require.Equal(t, "cust-1", body["customer"].(map[string]any)["customer_id"])
}

func TestCustomerHandlers_GetProfile_NotFound(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getProfileErr: status.Error(codes.NotFound, "profile not found")}
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).GetProfile(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "NOT_FOUND", decodeEnvelope(t, rec).Code)
}

func TestCustomerHandlers_UpdateProfile_Success(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{updateResp: &customerv1.UpdateProfileResponse{Customer: &customerv1.Customer{
		CustomerId: "cust-1", Status: customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED,
	}}}
	req := authRequest("/v1/customer/profile", `{
		"full_name":"Ada Lovelace","date_of_birth":"1990-01-02","address":"1 Main St",
		"phone":"+10000000000","citizenship":"US"
	}`)
	req.Method = http.MethodPut
	req.Header.Set("Idempotency-Key", "idem-9")
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).UpdateProfile(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Ada Lovelace", customer.lastUpdate.GetFullName())
	require.Equal(t, "1990-01-02", customer.lastUpdate.GetDateOfBirth())
	require.Equal(t, "US", customer.lastUpdate.GetCitizenship())
	require.Equal(t, "idem-9", customer.lastUpdate.GetIdempotencyKey())
}

func TestCustomerHandlers_UpdateProfile_MissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{}
	req := authRequest("/v1/customer/profile", `{"full_name":"Ada"}`)
	req.Method = http.MethodPut
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).UpdateProfile(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, customer.lastUpdate)
}

func TestCustomerHandlers_UpdateProfile_Locked(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{updateErr: status.Error(codes.FailedPrecondition, "profile is locked")}
	req := authRequest("/v1/customer/profile", `{"full_name":"Ada"}`)
	req.Method = http.MethodPut
	req.Header.Set("Idempotency-Key", "idem-9")
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).UpdateProfile(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "FAILED_PRECONDITION", decodeEnvelope(t, rec).Code)
}

func TestCustomerHandlers_GetStatus_ForwardsCustomerID(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getStatusResp: &customerv1.GetCustomerStatusResponse{
		CustomerId: "cust-1", Status: customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE,
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/status", nil)
	req = req.WithContext(interceptors.WithPrincipal(req.Context(), interceptors.Principal{
		Subject: "user-1", CustomerID: "cust-1", Roles: []string{RoleClient},
	}))
	rec := httptest.NewRecorder()
	NewCustomerHandlers(customer).GetStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cust-1", customer.lastGetStatus.GetCustomerId())
	require.Equal(t, "CUSTOMER_STATUS_ACTIVE", decodeBody(t, rec)["status"])
}

func TestRegisterCustomerRoutes_RequiresClient(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getProfileResp: &customerv1.GetProfileResponse{Customer: &customerv1.Customer{CustomerId: "cust-1"}}}
	mux := http.NewServeMux()
	RegisterCustomerRoutes(mux, NewCustomerHandlers(customer), authAs(RoleClient))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, customer.lastGetProfile)

	rec = httptest.NewRecorder()
	mux2 := http.NewServeMux()
	RegisterCustomerRoutes(mux2, NewCustomerHandlers(&fakeCustomer{}), authAs(RoleOfficer))
	mux2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func authAs(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := interceptors.WithPrincipal(r.Context(), interceptors.Principal{
				Subject: "user-1", Roles: []string{role},
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
