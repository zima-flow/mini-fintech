//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func TestIdempotencyRepo_PutGetAndDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	repo := postgres.NewIdempotencyRepo(pool)

	rec := domain.IdempotencyRecord{
		Scope:       "auth.register",
		Key:         "key-1",
		RequestHash: []byte{0x01, 0x02},
		Response:    []byte(`{"user_id":"u1"}`),
		CreatedAt:   testNow(),
	}
	require.NoError(t, repo.Put(ctx, rec))

	got, err := repo.Get(ctx, rec.Scope, rec.Key)
	require.NoError(t, err)
	require.Equal(t, rec.Scope, got.Scope)
	require.Equal(t, rec.Key, got.Key)
	require.Equal(t, rec.RequestHash, got.RequestHash)
	require.JSONEq(t, string(rec.Response), string(got.Response))
	require.Equal(t, rec.CreatedAt, got.CreatedAt)

	require.ErrorIs(t, repo.Put(ctx, rec), errs.ErrAlreadyExists)

	_, err = repo.Get(ctx, rec.Scope, "missing")
	require.ErrorIs(t, err, errs.ErrNotFound)
}
