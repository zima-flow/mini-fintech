package interceptors

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

const (
	testKID      = "test-kid"
	testIssuer   = "mini-fintech-auth"
	testAudience = "mini-fintech"
)

func TestAuthInterceptor(t *testing.T) {
	t.Parallel()

	key := mustEd25519Key(t)
	jwksURL := serveJWKS(t, key.Public().(ed25519.PublicKey), testKID)

	cfg := Config{
		Logger:    testLogger(),
		RequestID: id.UUIDv7{},
		Auth: AuthConfig{
			JWKSURL:       jwksURL,
			CacheTTL:      time.Minute,
			PublicMethods: []string{publicMethod},
			Issuer:        testIssuer,
			Audience:      testAudience,
		},
	}
	conn := serveProbe(t, Unary(cfg), Stream(cfg), nil)

	valid := signToken(t, key, testKID, time.Now().Add(5*time.Minute))
	expired := signToken(t, key, testKID, time.Now().Add(-time.Minute))
	wrongIssuer := signTokenClaims(t, key, testKID, time.Now().Add(5*time.Minute), "someone-else", testAudience)
	wrongAudience := signTokenClaims(t, key, testKID, time.Now().Add(5*time.Minute), testIssuer, "someone-else")

	cases := []struct {
		name  string
		token string
		want  codes.Code
	}{
		{name: "valid_token", token: valid, want: codes.OK},
		{name: "expired_token", token: expired, want: codes.Unauthenticated},
		{name: "malformed_token", token: "not.a.jwt", want: codes.Unauthenticated},
		{name: "missing_token", token: "", want: codes.Unauthenticated},
		{name: "wrong_issuer", token: wrongIssuer, want: codes.Unauthenticated},
		{name: "wrong_audience", token: wrongAudience, want: codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := invokeUnary(authContext(tc.token), conn, protectedMethod)
			require.Equal(t, tc.want, status.Code(err), "err = %v", err)
		})
	}

	t.Run("public_method_skips_auth", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, invokeUnary(context.Background(), conn, publicMethod))
	})

	t.Run("protected_stream_missing_token", func(t *testing.T) {
		t.Parallel()
		err := invokeStream(context.Background(), conn, publicStream)
		require.Equal(t, codes.Unauthenticated, status.Code(err), "err = %v", err)
	})

	t.Run("protected_stream_valid_token", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, invokeStream(authContext(valid), conn, publicStream))
	})

	t.Run("principal_carries_role_and_customer_id", func(t *testing.T) {
		t.Parallel()

		var got Principal
		principalConn := serveProbe(t, Unary(cfg), Stream(cfg), func(ctx context.Context) error {
			principal, ok := PrincipalFromContext(ctx)
			require.True(t, ok, "handler must see a principal")
			got = principal
			return nil
		})
		require.NoError(t, invokeUnary(authContext(valid), principalConn, protectedMethod))

		require.Equal(t, "user-1", got.Subject)
		require.Equal(t, []string{"CLIENT"}, got.Roles)
		require.Equal(t, "customer-1", got.CustomerID)
	})

	t.Run("jwks_unavailable", func(t *testing.T) {
		t.Parallel()
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "jwks down", http.StatusInternalServerError)
		}))
		t.Cleanup(down.Close)

		downCfg := cfg
		downCfg.Auth.JWKSURL = down.URL
		downConn := serveProbe(t, Unary(downCfg), Stream(downCfg), nil)

		err := invokeUnary(authContext(valid), downConn, protectedMethod)
		require.Equal(t, codes.Unavailable, status.Code(err), "err = %v", err)
	})

	t.Run("missing_issuer_audience_fails_closed", func(t *testing.T) {
		t.Parallel()

		badCfg := cfg
		badCfg.Auth.Issuer = ""
		badCfg.Auth.Audience = ""
		badConn := serveProbe(t, Unary(badCfg), Stream(badCfg), nil)

		err := invokeUnary(authContext(valid), badConn, protectedMethod)
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "err = %v", err)

		require.NoError(t, invokeUnary(context.Background(), badConn, publicMethod))
	})
}

func TestAuthConfigValidate(t *testing.T) {
	t.Parallel()

	require.Error(t, AuthConfig{}.Validate(), "no issuer/audience must be invalid")
	require.Error(t, AuthConfig{Issuer: testIssuer}.Validate(), "missing audience must be invalid")
	require.Error(t, AuthConfig{Audience: testAudience}.Validate(), "missing issuer must be invalid")
	require.NoError(t, AuthConfig{Issuer: testIssuer, Audience: testAudience}.Validate())
	require.NoError(t, AuthConfig{Dev: true}.Validate(), "dev skips the requirement")
}

func invokeUnary(ctx context.Context, conn *grpc.ClientConn, method string) error {
	return conn.Invoke(ctx, method, new(emptypb.Empty), new(emptypb.Empty))
}

func invokeStream(ctx context.Context, conn *grpc.ClientConn, method string) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{StreamName: "CallStream", ServerStreams: true}, method)
	if err != nil {
		return err
	}
	if err := stream.SendMsg(new(emptypb.Empty)); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	var msg emptypb.Empty
	for {
		if err := stream.RecvMsg(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func authContext(token string) context.Context {
	ctx := context.Background()
	if token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, authorization, bearerPrefix+token)
}

func mustEd25519Key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

func serveJWKS(t *testing.T, pub ed25519.PublicKey, kid string) string {
	t.Helper()

	jwkKey, err := jwk.Import(pub)
	require.NoError(t, err)
	require.NoError(t, jwkKey.Set(jwk.KeyIDKey, kid))
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

func signToken(t *testing.T, key ed25519.PrivateKey, kid string, expiry time.Time) string {
	t.Helper()
	return signTokenClaims(t, key, kid, expiry, testIssuer, testAudience)
}

func signTokenClaims(t *testing.T, key ed25519.PrivateKey, kid string, expiry time.Time, issuer, audience string) string {
	t.Helper()

	priv, err := jwk.Import(key)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, kid))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))

	token, err := jwt.NewBuilder().
		Issuer(issuer).
		Subject("user-1").
		Audience([]string{audience}).
		IssuedAt(time.Now()).
		Expiration(expiry).
		Claim("role", "CLIENT").
		Claim("customer_id", "customer-1").
		Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.EdDSA(), priv))
	require.NoError(t, err)
	return string(signed)
}
