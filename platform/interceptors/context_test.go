package interceptors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithAuthorization_RoundTrips(t *testing.T) {
	t.Parallel()

	ctx := WithAuthorization(context.Background(), "Bearer abc.def")
	require.Equal(t, "Bearer abc.def", AuthorizationFromContext(ctx))
}

func TestAuthorizationFromContext_EmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	require.Empty(t, AuthorizationFromContext(context.Background()))
}

func TestWithPrincipal_RoundTrips(t *testing.T) {
	t.Parallel()

	want := Principal{Subject: "user-1", Roles: []string{"CLIENT"}, CustomerID: "cust-1"}
	got, ok := PrincipalFromContext(WithPrincipal(context.Background(), want))
	require.True(t, ok)
	require.Equal(t, want, got)
}
