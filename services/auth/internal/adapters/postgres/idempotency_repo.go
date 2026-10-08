package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type IdempotencyRepo struct {
	db platformpostgres.DBTX
}

var _ domain.IdempotencyRepo = (*IdempotencyRepo)(nil)

func NewIdempotencyRepo(db platformpostgres.DBTX) *IdempotencyRepo {
	return &IdempotencyRepo{db: db}
}

func (r *IdempotencyRepo) Put(ctx context.Context, record domain.IdempotencyRecord) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO idempotency_keys (scope, key, request_hash, response, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		record.Scope, record.Key, record.RequestHash, record.Response, record.CreatedAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("put idempotency %s/%s: %w", record.Scope, record.Key, errs.ErrAlreadyExists)
		}
		return fmt.Errorf("put idempotency key: %w", err)
	}
	return nil
}

func (r *IdempotencyRepo) Get(ctx context.Context, scope, key string) (domain.IdempotencyRecord, error) {
	var (
		requestHash []byte
		response    []byte
		createdAt   time.Time
	)
	err := r.db.QueryRow(ctx,
		`SELECT request_hash, response, created_at FROM idempotency_keys WHERE scope = $1 AND key = $2`,
		scope, key,
	).Scan(&requestHash, &response, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.IdempotencyRecord{}, fmt.Errorf("idempotency %s/%s not found: %w", scope, key, errs.ErrNotFound)
		}
		return domain.IdempotencyRecord{}, fmt.Errorf("get idempotency key: %w", err)
	}

	return domain.IdempotencyRecord{
		Scope:       scope,
		Key:         key,
		RequestHash: requestHash,
		Response:    response,
		CreatedAt:   createdAt.UTC(),
	}, nil
}
