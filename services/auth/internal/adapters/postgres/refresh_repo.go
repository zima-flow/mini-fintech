package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const refreshTokenColumns = `id, user_id, token_hash, family_id, issued_at, expires_at, rotated_at, replaced_by_id, revoked_at, revoked_reason`

type RefreshTokenRepo struct {
	db platformpostgres.DBTX
}

var _ domain.RefreshTokenRepo = (*RefreshTokenRepo)(nil)

func NewRefreshTokenRepo(db platformpostgres.DBTX) *RefreshTokenRepo {
	return &RefreshTokenRepo{db: db}
}

func (r *RefreshTokenRepo) Create(ctx context.Context, token domain.RefreshToken) error {
	id, err := toPgUUID(token.ID)
	if err != nil {
		return err
	}
	userID, err := toPgUUID(token.UserID)
	if err != nil {
		return err
	}
	familyID, err := toPgUUID(token.FamilyID)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, userID, token.TokenHash, familyID, token.IssuedAt.UTC(), token.ExpiresAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("insert refresh token: %w", errs.ErrAlreadyExists)
		}
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepo) ByHashForUpdate(ctx context.Context, tokenHash []byte) (domain.RefreshToken, error) {
	return scanRefreshToken(r.db.QueryRow(ctx,
		`SELECT `+refreshTokenColumns+` FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, tokenHash))
}

func (r *RefreshTokenRepo) MarkRotated(ctx context.Context, id, successorID string, at time.Time) error {
	tokenID, err := toPgUUID(id)
	if err != nil {
		return err
	}
	successor, err := toPgUUID(successorID)
	if err != nil {
		return err
	}

	tag, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET rotated_at = $2, replaced_by_id = $3 WHERE id = $1`,
		tokenID, at.UTC(), successor,
	)
	if err != nil {
		return fmt.Errorf("mark refresh token rotated: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark refresh token %s rotated: %w", id, errs.ErrNotFound)
	}
	return nil
}

func (r *RefreshTokenRepo) RevokeFamily(ctx context.Context, familyID string, at time.Time, reason string) error {
	fid, err := toPgUUID(familyID)
	if err != nil {
		return err
	}

	if _, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2, revoked_reason = $3 WHERE family_id = $1 AND revoked_at IS NULL`,
		fid, at.UTC(), reason,
	); err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}
	return nil
}

func scanRefreshToken(row pgx.Row) (domain.RefreshToken, error) {
	var (
		id            pgtype.UUID
		userID        pgtype.UUID
		tokenHash     []byte
		familyID      pgtype.UUID
		issuedAt      time.Time
		expiresAt     time.Time
		rotatedAt     pgtype.Timestamptz
		replacedByID  pgtype.UUID
		revokedAt     pgtype.Timestamptz
		revokedReason pgtype.Text
	)
	if err := row.Scan(&id, &userID, &tokenHash, &familyID, &issuedAt, &expiresAt,
		&rotatedAt, &replacedByID, &revokedAt, &revokedReason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefreshToken{}, fmt.Errorf("refresh token not found: %w", errs.ErrNotFound)
		}
		return domain.RefreshToken{}, fmt.Errorf("scan refresh token: %w", err)
	}

	return domain.RefreshToken{
		ID:            fromPgUUID(id),
		UserID:        fromPgUUID(userID),
		TokenHash:     tokenHash,
		FamilyID:      fromPgUUID(familyID),
		IssuedAt:      issuedAt.UTC(),
		ExpiresAt:     expiresAt.UTC(),
		RotatedAt:     timestampPtr(rotatedAt),
		ReplacedByID:  fromPgUUID(replacedByID),
		RevokedAt:     timestampPtr(revokedAt),
		RevokedReason: revokedReason.String,
	}, nil
}
