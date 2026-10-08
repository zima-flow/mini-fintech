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

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	grpcadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/grpc"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const (
	testKID      = "test-kid"
	testIssuer   = "mini-fintech-auth"
	testAudience = "mini-fintech"
)

type fakeUseCases struct {
	registerFn func(context.Context, app.RegisterCommand) (app.RegisterResult, error)
	loginFn    func(context.Context, app.LoginCommand) (app.LoginResult, error)
	refreshFn  func(context.Context, app.RefreshCommand) (app.RefreshResult, error)
	logoutFn   func(context.Context, app.LogoutCommand) error
	validateFn func(context.Context, app.ValidateTokenCommand) (app.ValidateTokenResult, error)
	officerFn  func(context.Context, app.CreateOfficerCommand) (app.CreateOfficerResult, error)
}

func (f *fakeUseCases) Register(ctx context.Context, cmd app.RegisterCommand) (app.RegisterResult, error) {
	if f.registerFn == nil {
		return app.RegisterResult{}, nil
	}
	return f.registerFn(ctx, cmd)
}

func (f *fakeUseCases) Login(ctx context.Context, cmd app.LoginCommand) (app.LoginResult, error) {
	if f.loginFn == nil {
		return app.LoginResult{}, nil
	}
	return f.loginFn(ctx, cmd)
}

func (f *fakeUseCases) Refresh(ctx context.Context, cmd app.RefreshCommand) (app.RefreshResult, error) {
	if f.refreshFn == nil {
		return app.RefreshResult{}, nil
	}
	return f.refreshFn(ctx, cmd)
}

func (f *fakeUseCases) Logout(ctx context.Context, cmd app.LogoutCommand) error {
	if f.logoutFn == nil {
		return nil
	}
	return f.logoutFn(ctx, cmd)
}

func (f *fakeUseCases) ValidateToken(ctx context.Context, cmd app.ValidateTokenCommand) (app.ValidateTokenResult, error) {
	if f.validateFn == nil {
		return app.ValidateTokenResult{}, nil
	}
	return f.validateFn(ctx, cmd)
}

func (f *fakeUseCases) CreateOfficer(ctx context.Context, cmd app.CreateOfficerCommand) (app.CreateOfficerResult, error) {
	if f.officerFn == nil {
		return app.CreateOfficerResult{}, nil
	}
	return f.officerFn(ctx, cmd)
}

func (f *fakeUseCases) useCases() grpcadapter.UseCases {
	return grpcadapter.UseCases{
		Register:      f,
		Login:         f,
		Refresh:       f,
		Logout:        f,
		ValidateToken: f,
		CreateOfficer: f,
	}
}

func startServer(t *testing.T, auth interceptors.AuthConfig, uc grpcadapter.UseCases) authv1.AuthServiceClient {
	t.Helper()

	cfg := interceptors.Config{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestID: id.UUIDv7{},
		Auth:      auth,
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
	return authv1.NewAuthServiceClient(conn)
}

func publicOnlyCfg() interceptors.AuthConfig {
	return interceptors.AuthConfig{PublicMethods: grpcadapter.PublicMethods(), Dev: true}
}

func sampledContext(t *testing.T) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
}

func TestRegister_CapturesTraceHeaders(t *testing.T) {
	t.Parallel()

	var got app.RegisterCommand
	fake := &fakeUseCases{registerFn: func(_ context.Context, cmd app.RegisterCommand) (app.RegisterResult, error) {
		got = cmd
		return app.RegisterResult{UserID: "u-1", Role: domain.RoleClient}, nil
	}}
	srv := grpcadapter.NewServer(fake.useCases(), propagation.TraceContext{})

	_, err := srv.Register(sampledContext(t), &authv1.RegisterRequest{
		Email:          "Alice@Example.com",
		Password:       "correct horse battery",
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", got.Headers["traceparent"])
}

func TestRegister_MapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var got app.RegisterCommand
	fake := &fakeUseCases{registerFn: func(_ context.Context, cmd app.RegisterCommand) (app.RegisterResult, error) {
		got = cmd
		return app.RegisterResult{UserID: "u-1", Role: domain.RoleClient}, nil
	}}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	resp, err := client.Register(context.Background(), &authv1.RegisterRequest{
		Email:          "Alice@Example.com",
		Password:       "correct horse battery",
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)
	require.Equal(t, "u-1", resp.GetUserId())
	require.Equal(t, authv1.Role_ROLE_CLIENT, resp.GetRole())
	require.Empty(t, resp.GetCustomerId(), "customer_id stays empty until linked")

	require.Equal(t, "Alice@Example.com", got.Email)
	require.Equal(t, "correct horse battery", got.Password)
	require.Equal(t, "key-1", got.IdempotencyKey)
}

func TestLogin_MapsTokenPair(t *testing.T) {
	t.Parallel()

	expires := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	var got app.LoginCommand
	fake := &fakeUseCases{loginFn: func(_ context.Context, cmd app.LoginCommand) (app.LoginResult, error) {
		got = cmd
		return app.LoginResult{
			Tokens:     domain.TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: expires},
			UserID:     "u-1",
			Role:       domain.RoleOfficer,
			CustomerID: "c-1",
		}, nil
	}}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	resp, err := client.Login(context.Background(), &authv1.LoginRequest{Email: "a@example.com", Password: "pw"})
	require.NoError(t, err)
	require.Equal(t, "access", resp.GetTokens().GetAccessToken())
	require.Equal(t, "refresh", resp.GetTokens().GetRefreshToken())
	require.True(t, resp.GetTokens().GetAccessTokenExpiresAt().AsTime().Equal(expires))
	require.Equal(t, authv1.Role_ROLE_OFFICER, resp.GetRole())
	require.Equal(t, "u-1", resp.GetUserId())
	require.Equal(t, "c-1", resp.GetCustomerId())
	require.Equal(t, "a@example.com", got.Email)
	require.Equal(t, "pw", got.Password)
}

func TestRefresh_MapsTokenPair(t *testing.T) {
	t.Parallel()

	var got app.RefreshCommand
	fake := &fakeUseCases{refreshFn: func(_ context.Context, cmd app.RefreshCommand) (app.RefreshResult, error) {
		got = cmd
		return app.RefreshResult{Tokens: domain.TokenPair{AccessToken: "access", RefreshToken: "rotated"}}, nil
	}}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	resp, err := client.Refresh(context.Background(), &authv1.RefreshRequest{RefreshToken: "old"})
	require.NoError(t, err)
	require.Equal(t, "access", resp.GetTokens().GetAccessToken())
	require.Equal(t, "rotated", resp.GetTokens().GetRefreshToken())
	require.Equal(t, "old", got.RefreshToken)
}

func TestLogout_PassesRefreshToken(t *testing.T) {
	t.Parallel()

	var got app.LogoutCommand
	fake := &fakeUseCases{logoutFn: func(_ context.Context, cmd app.LogoutCommand) error {
		got = cmd
		return nil
	}}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	_, err := client.Logout(context.Background(), &authv1.LogoutRequest{RefreshToken: "refresh"})
	require.NoError(t, err)
	require.Equal(t, "refresh", got.RefreshToken)
}

func TestPublicMethods_AreClassified(t *testing.T) {
	t.Parallel()

	public := grpcadapter.PublicMethods()
	require.Contains(t, public, authv1.AuthService_Register_FullMethodName)
	require.Contains(t, public, authv1.AuthService_Login_FullMethodName)
	require.Contains(t, public, authv1.AuthService_Refresh_FullMethodName)
	require.Contains(t, public, authv1.AuthService_Logout_FullMethodName)
	require.NotContains(t, public, authv1.AuthService_ValidateToken_FullMethodName)
	require.NotContains(t, public, authv1.AuthService_CreateOfficer_FullMethodName)
}

func TestProtectedWithoutToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	called := false
	fake := &fakeUseCases{
		validateFn: func(context.Context, app.ValidateTokenCommand) (app.ValidateTokenResult, error) {
			called = true
			return app.ValidateTokenResult{}, nil
		},
		officerFn: func(context.Context, app.CreateOfficerCommand) (app.CreateOfficerResult, error) {
			called = true
			return app.CreateOfficerResult{}, nil
		},
	}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	_, err := client.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{AccessToken: "x"})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)

	_, err = client.CreateOfficer(context.Background(), &authv1.CreateOfficerRequest{Email: "o@example.com"})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)

	require.False(t, called, "the handler must not run for an unauthenticated protected call")
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	fake := &fakeUseCases{
		registerFn: func(context.Context, app.RegisterCommand) (app.RegisterResult, error) {
			return app.RegisterResult{}, fmt.Errorf("register: bad input: %w", errs.ErrInvalidArgument)
		},
		loginFn: func(context.Context, app.LoginCommand) (app.LoginResult, error) {
			return app.LoginResult{}, fmt.Errorf("login: nope: %w", errs.ErrUnauthenticated)
		},
		refreshFn: func(context.Context, app.RefreshCommand) (app.RefreshResult, error) {
			return app.RefreshResult{}, errors.New("boom: secret detail")
		},
	}
	client := startServer(t, publicOnlyCfg(), fake.useCases())

	_, err := client.Register(context.Background(), &authv1.RegisterRequest{Email: "a@example.com"})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "err = %v", err)

	_, err = client.Login(context.Background(), &authv1.LoginRequest{Email: "a@example.com"})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)

	_, err = client.Refresh(context.Background(), &authv1.RefreshRequest{RefreshToken: "r"})
	require.Equal(t, codes.Internal, status.Code(err), "err = %v", err)
	require.NotContains(t, status.Convert(err).Message(), "secret detail", "internal details must not leak")
}

func TestCreateOfficer_Authenticated_PassesActorRole(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	cfg := interceptors.AuthConfig{
		JWKSURL:       serveJWKS(t, key.Public().(ed25519.PublicKey)),
		Issuer:        testIssuer,
		Audience:      testAudience,
		PublicMethods: grpcadapter.PublicMethods(),
	}

	var got app.CreateOfficerCommand
	fake := &fakeUseCases{officerFn: func(_ context.Context, cmd app.CreateOfficerCommand) (app.CreateOfficerResult, error) {
		got = cmd
		if cmd.ActorRole != domain.RoleAdmin {
			return app.CreateOfficerResult{}, fmt.Errorf("create officer: not admin: %w", errs.ErrPermissionDenied)
		}
		return app.CreateOfficerResult{UserID: "o-1", Role: domain.RoleOfficer}, nil
	}}
	client := startServer(t, cfg, fake.useCases())

	adminToken := signToken(t, key, "ADMIN")
	resp, err := client.CreateOfficer(authContext(adminToken), &authv1.CreateOfficerRequest{
		Email:          "officer@example.com",
		Password:       "correct horse battery",
		IdempotencyKey: "key-2",
	})
	require.NoError(t, err)
	require.Equal(t, "o-1", resp.GetUserId())
	require.Equal(t, authv1.Role_ROLE_OFFICER, resp.GetRole())
	require.Equal(t, domain.RoleAdmin, got.ActorRole)
	require.Equal(t, "key-2", got.IdempotencyKey)

	clientToken := signToken(t, key, "CLIENT")
	_, err = client.CreateOfficer(authContext(clientToken), &authv1.CreateOfficerRequest{Email: "officer@example.com"})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "err = %v", err)
	require.Equal(t, domain.RoleClient, got.ActorRole)
}

func TestValidateToken_Authenticated_ReturnsClaims(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	cfg := interceptors.AuthConfig{
		JWKSURL:       serveJWKS(t, key.Public().(ed25519.PublicKey)),
		Issuer:        testIssuer,
		Audience:      testAudience,
		PublicMethods: grpcadapter.PublicMethods(),
	}

	expires := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	fake := &fakeUseCases{validateFn: func(_ context.Context, cmd app.ValidateTokenCommand) (app.ValidateTokenResult, error) {
		require.Equal(t, "the-access-token", cmd.AccessToken)
		return app.ValidateTokenResult{
			UserID:     "u-1",
			Role:       domain.RoleOfficer,
			CustomerID: "c-1",
			ExpiresAt:  expires,
		}, nil
	}}
	client := startServer(t, cfg, fake.useCases())

	resp, err := client.ValidateToken(authContext(signToken(t, key, "OFFICER")), &authv1.ValidateTokenRequest{
		AccessToken: "the-access-token",
	})
	require.NoError(t, err)
	require.True(t, resp.GetValid())
	require.Equal(t, "u-1", resp.GetUserId())
	require.Equal(t, authv1.Role_ROLE_OFFICER, resp.GetRole())
	require.Equal(t, "c-1", resp.GetCustomerId())
	require.True(t, resp.GetExpiresAt().AsTime().Equal(expires))
}

// --- helpers ---

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

func signToken(t *testing.T, key ed25519.PrivateKey, role string) string {
	t.Helper()

	priv, err := jwk.Import(key)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, testKID))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))

	token, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Subject("user-1").
		Audience([]string{testAudience}).
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(5*time.Minute)).
		Claim("role", role).
		Claim("customer_id", "customer-1").
		Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.EdDSA(), priv))
	require.NoError(t, err)
	return string(signed)
}
