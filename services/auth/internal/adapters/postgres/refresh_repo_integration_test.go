//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func newRefreshToken(userID, familyID string) domain.RefreshToken {
	return domain.RefreshToken{
		ID:        id.UUIDv7{}.New(),
		UserID:    userID,
		TokenHash: []byte(id.UUIDv7{}.New()),
		FamilyID:  familyID,
		IssuedAt:  testNow(),
		ExpiresAt: testNow().Add(30 * 24 * time.Hour),
	}
}

func TestRefreshTokenRepo_Lifecycle(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	user := seedUser(t, pool)
	repo := postgres.NewRefreshTokenRepo(pool)

	family := id.UUIDv7{}.New()
	token := newRefreshToken(user.ID, family)
	require.NoError(t, repo.Create(ctx, token))

	got, err := repo.ByHashForUpdate(ctx, token.TokenHash)
	require.NoError(t, err)
	require.Equal(t, token, got)
	require.False(t, got.Rotated())
	require.False(t, got.Revoked())

	successor := id.UUIDv7{}.New()
	rotatedAt := testNow().Add(time.Minute)
	require.NoError(t, repo.MarkRotated(ctx, token.ID, successor, rotatedAt))

	got, err = repo.ByHashForUpdate(ctx, token.TokenHash)
	require.NoError(t, err)
	require.True(t, got.Rotated())
	require.Equal(t, successor, got.ReplacedByID)
	require.NotNil(t, got.RotatedAt)
	require.Equal(t, rotatedAt, got.RotatedAt.UTC())

	revokedAt := testNow().Add(2 * time.Minute)
	require.NoError(t, repo.RevokeFamily(ctx, family, revokedAt, "reuse detected"))
	// A second revocation (e.g. a repeated logout) must not rewrite the record.
	require.NoError(t, repo.RevokeFamily(ctx, family, revokedAt.Add(time.Hour), "second"))

	got, err = repo.ByHashForUpdate(ctx, token.TokenHash)
	require.NoError(t, err)
	require.True(t, got.Revoked())
	require.Equal(t, "reuse detected", got.RevokedReason)
	require.NotNil(t, got.RevokedAt)
	require.Equal(t, revokedAt, got.RevokedAt.UTC())

	_, err = repo.ByHashForUpdate(ctx, []byte("unknown-hash"))
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestRefreshTokenRepo_ConcurrentLock_SecondReaderSeesRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := startAuthPostgres(t, ctx)
	user := seedUser(t, pool)
	repo := postgres.NewRefreshTokenRepo(pool)
	tx := postgres.NewTransactor(pool)

	family := id.UUIDv7{}.New()
	token := newRefreshToken(user.ID, family)
	require.NoError(t, repo.Create(ctx, token))

	locked := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer release()

	tx1 := make(chan error, 1)
	go func() {
		tx1 <- tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
			if _, err := uow.RefreshTokens().ByHashForUpdate(ctx, token.TokenHash); err != nil {
				return err
			}
			close(locked)
			<-releaseCh
			return uow.RefreshTokens().MarkRotated(ctx, token.ID, id.UUIDv7{}.New(), testNow().Add(time.Minute))
		})
	}()

	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal("tx1 never acquired the row lock")
	}

	type result struct {
		token domain.RefreshToken
		err   error
	}
	tx2 := make(chan result, 1)
	go func() {
		var r result
		r.err = tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
			got, err := uow.RefreshTokens().ByHashForUpdate(ctx, token.TokenHash)
			r.token = got
			return err
		})
		tx2 <- r
	}()

	require.Eventually(t, func() bool {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			return false
		}
		return waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "tx2 must be waiting on tx1's row lock")

	release()
	require.NoError(t, <-tx1)

	select {
	case r := <-tx2:
		require.NoError(t, r.err)
		require.True(t, r.token.Rotated(), "the blocked reader sees the committed rotation")
		require.NotEmpty(t, r.token.ReplacedByID)
	case <-ctx.Done():
		t.Fatal("tx2 never returned")
	}
}
