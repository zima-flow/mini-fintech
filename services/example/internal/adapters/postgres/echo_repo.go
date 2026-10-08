package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/domain"
)

const uniqueViolation = "23505"

type EchoRepo struct {
	db  platformpostgres.DBTX
	ids id.Generator
}

func NewEchoRepo(db platformpostgres.DBTX, ids id.Generator) *EchoRepo {
	return &EchoRepo{db: db, ids: ids}
}

func (r *EchoRepo) Echo(ctx context.Context, message string, at time.Time) (domain.Echo, error) {
	generated, err := uuid.Parse(r.ids.New())
	if err != nil {
		return domain.Echo{}, fmt.Errorf("generate echo id: %w", err)
	}
	pgID := pgtype.UUID{Bytes: generated, Valid: true}

	_, err = r.db.Exec(ctx,
		"INSERT INTO echoes (id, message, created_at) VALUES ($1, $2, $3)",
		pgID, message, at.UTC(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return domain.Echo{}, fmt.Errorf("insert echo %q: %w", message, errs.ErrAlreadyExists)
		}
		return domain.Echo{}, fmt.Errorf("insert echo: %w", err)
	}

	var (
		storedID      pgtype.UUID
		storedMessage string
		storedAt      time.Time
	)
	err = r.db.QueryRow(ctx,
		"SELECT id, message, created_at FROM echoes WHERE id = $1", pgID,
	).Scan(&storedID, &storedMessage, &storedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Echo{}, fmt.Errorf("select echo %s: %w", generated, errs.ErrNotFound)
		}
		return domain.Echo{}, fmt.Errorf("select echo: %w", err)
	}

	return domain.Echo{
		ID:      uuid.UUID(storedID.Bytes).String(),
		Message: storedMessage,
		At:      storedAt.UTC(),
	}, nil
}
