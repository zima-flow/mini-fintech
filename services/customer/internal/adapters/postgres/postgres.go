package postgres

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

const uniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func toPgUUID(s string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("postgres: invalid uuid %q: %w", s, errs.ErrInvalidArgument)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func fromPgUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
