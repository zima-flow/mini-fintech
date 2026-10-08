package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func TestRefreshToken_Reusable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)

	tests := []struct {
		name  string
		token domain.RefreshToken
		want  bool
	}{
		{
			name:  "fresh and unexpired",
			token: domain.RefreshToken{ExpiresAt: now.Add(time.Hour)},
			want:  true,
		},
		{
			name:  "expired",
			token: domain.RefreshToken{ExpiresAt: now.Add(-time.Second)},
			want:  false,
		},
		{
			name:  "expires exactly now",
			token: domain.RefreshToken{ExpiresAt: now},
			want:  false,
		},
		{
			name:  "already rotated",
			token: domain.RefreshToken{ExpiresAt: now.Add(time.Hour), RotatedAt: &past},
			want:  false,
		},
		{
			name:  "revoked",
			token: domain.RefreshToken{ExpiresAt: now.Add(time.Hour), RevokedAt: &past},
			want:  false,
		},
		{
			name:  "rotated and revoked",
			token: domain.RefreshToken{ExpiresAt: now.Add(time.Hour), RotatedAt: &past, RevokedAt: &past},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.token.Reusable(now))
		})
	}
}

func TestRefreshToken_StatePredicates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute)

	fresh := domain.RefreshToken{ExpiresAt: now.Add(time.Hour)}
	require.False(t, fresh.Rotated())
	require.False(t, fresh.Revoked())
	require.False(t, fresh.Expired(now))

	rotated := domain.RefreshToken{RotatedAt: &at}
	require.True(t, rotated.Rotated())

	revoked := domain.RefreshToken{RevokedAt: &at}
	require.True(t, revoked.Revoked())

	expired := domain.RefreshToken{ExpiresAt: now}
	require.True(t, expired.Expired(now))
}

func TestHashRefreshToken(t *testing.T) {
	t.Parallel()

	secret := "opaque-refresh-secret"
	hash := domain.HashRefreshToken(secret)

	require.Len(t, hash, 32, "SHA-256 produces a 32-byte digest")
	require.Equal(t, hash, domain.HashRefreshToken(secret), "hashing is deterministic")
	require.NotEqual(t, []byte(secret), hash)
	require.NotEqual(t, hash, domain.HashRefreshToken(secret+"x"))
}
