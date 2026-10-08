package grpcadapter_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	commonv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/common/v1"
	customerv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/grpc"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

const (
	testKID      = "test-kid"
	testIssuer   = "mini-fintech-auth"
	testAudience = "mini-fintech"
)

type fakeCustomerRepo struct {
	mu   sync.Mutex
	byID map[string]domain.Customer
}

var _ domain.CustomerRepo = (*fakeCustomerRepo)(nil)

func newFakeCustomerRepo(customers ...domain.Customer) *fakeCustomerRepo {
	r := &fakeCustomerRepo{byID: make(map[string]domain.Customer, len(customers))}
	for _, c := range customers {
		r.byID[c.ID] = c
	}
	return r
}

func (r *fakeCustomerRepo) Create(_ context.Context, c domain.Customer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.byID {
		if existing.UserID == c.UserID {
			return errs.ErrAlreadyExists
		}
	}
	r.byID[c.ID] = c
	return nil
}

func (r *fakeCustomerRepo) ByUserID(_ context.Context, userID string) (domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.byID {
		if c.UserID == userID {
			return c, nil
		}
	}
	return domain.Customer{}, errs.ErrNotFound
}

func (r *fakeCustomerRepo) ByID(_ context.Context, customerID string) (domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[customerID]
	if !ok {
		return domain.Customer{}, errs.ErrNotFound
	}
	return c, nil
}

func (r *fakeCustomerRepo) Update(_ context.Context, c domain.Customer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[c.ID]; !ok {
		return errs.ErrNotFound
	}
	r.byID[c.ID] = c
	return nil
}

func (r *fakeCustomerRepo) List(_ context.Context, filter domain.CustomerFilter) ([]domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var all []domain.Customer
	for _, c := range r.byID {
		if filter.Status == "" || c.Status == filter.Status {
			all = append(all, c)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	if filter.Offset >= len(all) {
		return nil, nil
	}
	all = all[filter.Offset:]
	if filter.Limit > 0 && filter.Limit < len(all) {
		all = all[:filter.Limit]
	}
	return all, nil
}

type stubUpdater struct {
	fn func(context.Context, app.UpdateProfileCommand) (app.UpdateProfileResult, error)
}

func (s stubUpdater) UpdateProfile(ctx context.Context, cmd app.UpdateProfileCommand) (app.UpdateProfileResult, error) {
	return s.fn(ctx, cmd)
}

func useCases(repo domain.CustomerRepo, updater grpcadapter.ProfileUpdater) grpcadapter.UseCases {
	return grpcadapter.UseCases{
		GetProfile:        app.NewGetProfileUseCase(repo),
		UpdateProfile:     updater,
		GetCustomer:       app.NewGetCustomerUseCase(repo),
		GetCustomerStatus: app.NewGetCustomerStatusUseCase(repo),
		ListCustomers:     app.NewListCustomersUseCase(repo),
	}
}

func testNow() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

func seedProfile() domain.Customer {
	return domain.Customer{
		ID:          "customer-1",
		UserID:      "user-1",
		FullName:    "Ada Lovelace",
		DateOfBirth: "1815-12-10",
		Address:     "12 Analytical Engine Way",
		Phone:       "+15550100",
		Citizenship: "GB",
		Status:      domain.StatusProfileFilled,
		CreatedAt:   testNow(),
		UpdatedAt:   testNow(),
	}
}

func startServer(t *testing.T, key ed25519.PrivateKey, uc grpcadapter.UseCases) customerv1.CustomerServiceClient {
	t.Helper()

	cfg := interceptors.Config{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestID: id.UUIDv7{},
		Auth: interceptors.AuthConfig{
			JWKSURL:  serveJWKS(t, key.Public().(ed25519.PublicKey)),
			Issuer:   testIssuer,
			Audience: testAudience,
		},
	}
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptors.Unary(cfg)...),
		grpc.ChainStreamInterceptor(interceptors.Stream(cfg)...),
	)
	grpcadapter.Register(srv, grpcadapter.NewServer(uc, nil))

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return customerv1.NewCustomerServiceClient(conn)
}

type fakeProfileUpdater struct {
	cmd app.UpdateProfileCommand
}

func (f *fakeProfileUpdater) UpdateProfile(_ context.Context, cmd app.UpdateProfileCommand) (app.UpdateProfileResult, error) {
	f.cmd = cmd
	return app.UpdateProfileResult{}, nil
}

func sampledContext(t *testing.T) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
	return interceptors.WithPrincipal(ctx, interceptors.Principal{Subject: "user-1", Roles: []string{"CLIENT"}})
}

func TestUpdateProfile_CapturesTraceHeaders(t *testing.T) {
	t.Parallel()

	updater := &fakeProfileUpdater{}
	srv := grpcadapter.NewServer(grpcadapter.UseCases{UpdateProfile: updater}, propagation.TraceContext{})

	_, err := srv.UpdateProfile(sampledContext(t), &customerv1.UpdateProfileRequest{
		FullName:       "Ada Lovelace",
		DateOfBirth:    "1990-01-02",
		Address:        "1 Analytical Engine Way",
		Phone:          "+15551234567",
		Citizenship:    "GB",
		IdempotencyKey: "idem-1",
	})
	require.NoError(t, err)
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", updater.cmd.Headers["traceparent"])
}

func TestGetProfile_OwnerReturnsOwnProfile(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	resp, err := client.GetProfile(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.GetProfileRequest{})
	require.NoError(t, err)

	c := resp.GetCustomer()
	require.Equal(t, "customer-1", c.GetCustomerId())
	require.Equal(t, "user-1", c.GetUserId())
	require.Equal(t, "Ada Lovelace", c.GetFullName())
	require.Equal(t, "1815-12-10", c.GetDateOfBirth())
	require.Equal(t, "12 Analytical Engine Way", c.GetAddress())
	require.Equal(t, "+15550100", c.GetPhone())
	require.Equal(t, "GB", c.GetCitizenship())
	require.Equal(t, customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED, c.GetStatus())
	require.True(t, c.GetCreatedAt().AsTime().Equal(testNow()))
	require.True(t, c.GetUpdatedAt().AsTime().Equal(testNow()))
}

func TestGetProfile_UnknownProfile_NotFound(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(), neverUpdater()))

	_, err := client.GetProfile(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.GetProfileRequest{})
	require.Equal(t, codes.NotFound, status.Code(err), "err = %v", err)
}

func TestUpdateProfile_MapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	var got app.UpdateProfileCommand
	updater := stubUpdater{fn: func(_ context.Context, cmd app.UpdateProfileCommand) (app.UpdateProfileResult, error) {
		got = cmd
		return app.UpdateProfileResult{Customer: seedProfile()}, nil
	}}
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), updater))

	resp, err := client.UpdateProfile(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.UpdateProfileRequest{
		FullName:       "Ada Lovelace",
		DateOfBirth:    "1815-12-10",
		Address:        "12 Analytical Engine Way",
		Phone:          "+15550100",
		Citizenship:    "GB",
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)
	require.Equal(t, "customer-1", resp.GetCustomer().GetCustomerId())

	require.Equal(t, "user-1", got.UserID)
	require.Equal(t, "Ada Lovelace", got.FullName)
	require.Equal(t, "1815-12-10", got.DateOfBirth)
	require.Equal(t, "12 Analytical Engine Way", got.Address)
	require.Equal(t, "+15550100", got.Phone)
	require.Equal(t, "GB", got.Citizenship)
	require.Equal(t, "key-1", got.IdempotencyKey)
}

func TestGetCustomer_Client_PermissionDenied(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	_, err := client.GetCustomer(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.GetCustomerRequest{
		CustomerId: "customer-1",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "err = %v", err)
}

func TestGetCustomer_Officer_ReturnsCustomer(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	resp, err := client.GetCustomer(authContext(signToken(t, key, "officer-1", "OFFICER")), &customerv1.GetCustomerRequest{
		CustomerId: "customer-1",
	})
	require.NoError(t, err)
	require.Equal(t, "customer-1", resp.GetCustomer().GetCustomerId())
	require.Equal(t, "Ada Lovelace", resp.GetCustomer().GetFullName())
}

func TestListCustomers_Client_PermissionDenied(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	_, err := client.ListCustomers(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.ListCustomersRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "err = %v", err)
}

func TestListCustomers_Officer_Paginates(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	first := seedProfile()
	second := domain.Customer{
		ID: "customer-2", UserID: "user-2", FullName: "Grace Hopper",
		Status: domain.StatusNew, CreatedAt: testNow().Add(time.Minute), UpdatedAt: testNow().Add(time.Minute),
	}
	client := startServer(t, key, useCases(newFakeCustomerRepo(first, second), neverUpdater()))
	token := authContext(signToken(t, key, "officer-1", "OFFICER"))

	page1, err := client.ListCustomers(token, &customerv1.ListCustomersRequest{
		Pagination: &commonv1.PaginationRequest{PageSize: 1},
	})
	require.NoError(t, err)
	require.Len(t, page1.GetCustomers(), 1)
	require.Equal(t, "customer-1", page1.GetCustomers()[0].GetCustomerId())
	require.NotEmpty(t, page1.GetPagination().GetNextPageToken())

	page2, err := client.ListCustomers(token, &customerv1.ListCustomersRequest{
		Pagination: &commonv1.PaginationRequest{PageSize: 1, PageToken: page1.GetPagination().GetNextPageToken()},
	})
	require.NoError(t, err)
	require.Len(t, page2.GetCustomers(), 1)
	require.Equal(t, "customer-2", page2.GetCustomers()[0].GetCustomerId())
	require.Empty(t, page2.GetPagination().GetNextPageToken())

	filtered, err := client.ListCustomers(token, &customerv1.ListCustomersRequest{
		Status: customerv1.CustomerStatus_CUSTOMER_STATUS_NEW,
	})
	require.NoError(t, err)
	require.Len(t, filtered.GetCustomers(), 1)
	require.Equal(t, "customer-2", filtered.GetCustomers()[0].GetCustomerId())
}

func TestGetCustomerStatus_ClientIgnoresSuppliedID(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	other := domain.Customer{
		ID: "customer-2", UserID: "user-2", Status: domain.StatusActive,
		CreatedAt: testNow(), UpdatedAt: testNow(),
	}
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile(), other), neverUpdater()))

	resp, err := client.GetCustomerStatus(authContext(signToken(t, key, "user-1", "CLIENT")), &customerv1.GetCustomerStatusRequest{
		CustomerId: "customer-2",
	})
	require.NoError(t, err)
	require.Equal(t, "customer-1", resp.GetCustomerId())
	require.Equal(t, customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED, resp.GetStatus())
}

func TestGetCustomerStatus_Officer_ReturnsRequested(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	resp, err := client.GetCustomerStatus(authContext(signToken(t, key, "officer-1", "OFFICER")), &customerv1.GetCustomerStatusRequest{
		CustomerId: "customer-1",
	})
	require.NoError(t, err)
	require.Equal(t, "customer-1", resp.GetCustomerId())
	require.Equal(t, customerv1.CustomerStatus_CUSTOMER_STATUS_PROFILE_FILLED, resp.GetStatus())
}

func TestProtectedWithoutToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), neverUpdater()))

	_, err := client.GetProfile(context.Background(), &customerv1.GetProfileRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)

	_, err = client.ListCustomers(context.Background(), &customerv1.ListCustomersRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	updater := stubUpdater{fn: func(_ context.Context, cmd app.UpdateProfileCommand) (app.UpdateProfileResult, error) {
		if cmd.IdempotencyKey == "bad-input" {
			return app.UpdateProfileResult{}, fmt.Errorf("update: bad: %w", errs.ErrInvalidArgument)
		}
		return app.UpdateProfileResult{}, errors.New("boom: secret detail")
	}}
	client := startServer(t, key, useCases(newFakeCustomerRepo(seedProfile()), updater))
	token := authContext(signToken(t, key, "user-1", "CLIENT"))

	_, err := client.UpdateProfile(token, &customerv1.UpdateProfileRequest{IdempotencyKey: "bad-input"})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "err = %v", err)

	_, err = client.UpdateProfile(token, &customerv1.UpdateProfileRequest{IdempotencyKey: "boom"})
	require.Equal(t, codes.Internal, status.Code(err), "err = %v", err)
	require.NotContains(t, status.Convert(err).Message(), "secret detail", "internal details must not leak")
}

func neverUpdater() grpcadapter.ProfileUpdater {
	return stubUpdater{fn: func(context.Context, app.UpdateProfileCommand) (app.UpdateProfileResult, error) {
		return app.UpdateProfileResult{}, errors.New("UpdateProfile must not be called")
	}}
}

// --- auth helpers ---

func authContext(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func mustEd25519Key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

func serveJWKS(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()

	jwkKey, err := jwk.Import(pub)
	require.NoError(t, err)
	require.NoError(t, jwkKey.Set(jwk.KeyIDKey, testKID))
	require.NoError(t, jwkKey.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))

	set := jwk.NewSet()
	require.NoError(t, set.AddKey(jwkKey))
	body, err := json.Marshal(set)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func signToken(t *testing.T, key ed25519.PrivateKey, subject, role string) string {
	t.Helper()

	priv, err := jwk.Import(key)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, testKID))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))

	token, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Subject(subject).
		Audience([]string{testAudience}).
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(5*time.Minute)).
		Claim("role", role).
		Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.EdDSA(), priv))
	require.NoError(t, err)
	return string(signed)
}
