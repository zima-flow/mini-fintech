package gatewayhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/common/v1"
	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
)

func TestOfficerHandlers_GetCustomer_Success(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getCustomerResp: &customerv1.GetCustomerResponse{Customer: &customerv1.Customer{
		CustomerId: "cust-9", Status: customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE,
	}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/officers/customers/cust-9", nil)
	req.SetPathValue("customer_id", "cust-9")
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).GetCustomer(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cust-9", customer.lastGetCustomer.GetCustomerId())
	require.Equal(t, "cust-9", decodeBody(t, rec)["customer"].(map[string]any)["customer_id"])
}

func TestOfficerHandlers_GetCustomer_MissingID(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{}
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).GetCustomer(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers/", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, customer.lastGetCustomer)
}

func TestOfficerHandlers_GetCustomer_Forbidden(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{getCustomerErr: status.Error(codes.PermissionDenied, "officer role required")}
	req := httptest.NewRequest(http.MethodGet, "/v1/officers/customers/cust-9", nil)
	req.SetPathValue("customer_id", "cust-9")
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).GetCustomer(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "PERMISSION_DENIED", decodeEnvelope(t, rec).Code)
}

func TestOfficerHandlers_ListCustomers_ParsesQuery(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{listResp: &customerv1.ListCustomersResponse{
		Customers:  []*customerv1.Customer{{CustomerId: "cust-1"}},
		Pagination: &commonv1.PaginationResponse{NextPageToken: "next-1"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/officers/customers?status=CUSTOMER_STATUS_ACTIVE&page_size=25&page_token=abc", nil)
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).ListCustomers(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE, customer.lastList.GetStatus())
	require.Equal(t, int32(25), customer.lastList.GetPagination().GetPageSize())
	require.Equal(t, "abc", customer.lastList.GetPagination().GetPageToken())

	body := decodeBody(t, rec)
	require.Equal(t, "next-1", body["pagination"].(map[string]any)["next_page_token"])
}

func TestOfficerHandlers_ListCustomers_DefaultsWhenOmitted(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{}
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).ListCustomers(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, customerv1.CustomerStatus_CUSTOMER_STATUS_UNSPECIFIED, customer.lastList.GetStatus())
	require.Equal(t, int32(0), customer.lastList.GetPagination().GetPageSize())
}

func TestOfficerHandlers_ListCustomers_InvalidStatus(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{}
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).ListCustomers(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers?status=NOPE", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, customer.lastList)
}

func TestOfficerHandlers_ListCustomers_InvalidPageSize(t *testing.T) {
	t.Parallel()

	customer := &fakeCustomer{}
	rec := httptest.NewRecorder()
	NewOfficerHandlers(customer).ListCustomers(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers?page_size=many", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, customer.lastList)
}

func TestRegisterOfficerRoutes_RequiresStaff(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	RegisterOfficerRoutes(mux, NewOfficerHandlers(&fakeCustomer{
		getCustomerResp: &customerv1.GetCustomerResponse{Customer: &customerv1.Customer{CustomerId: "cust-9"}},
	}), authAs(RoleOfficer))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers/cust-9", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	clientMux := http.NewServeMux()
	RegisterOfficerRoutes(clientMux, NewOfficerHandlers(&fakeCustomer{}), authAs(RoleClient))
	clientMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/officers/customers/cust-9", nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}
