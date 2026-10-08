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

const userColumns = `id, email, password_hash, role, customer_id, created_at, updated_at`

type UserRepo struct {
	db platformpostgres.DBTX
}

var _ domain.UserRepo = (*UserRepo)(nil)

func NewUserRepo(db platformpostgres.DBTX) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, user domain.User) error {
	id, err := toPgUUID(user.ID)
	if err != nil {
		return err
	}
	customerID, err := optionalUUID(user.CustomerID)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, role, customer_id, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, user.Email, user.PasswordHash, string(user.Role), customerID, user.CreatedAt.UTC(), user.UpdatedAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("insert user %q: %w", user.Email, errs.ErrAlreadyExists)
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *UserRepo) ByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func (r *UserRepo) ByID(ctx context.Context, id string) (domain.User, error) {
	uid, err := toPgUUID(id)
	if err != nil {
		return domain.User{}, err
	}
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, uid))
}

func (r *UserRepo) SetCustomerID(ctx context.Context, userID, customerID string, at time.Time) error {
	uid, err := toPgUUID(userID)
	if err != nil {
		return err
	}
	cid, err := toPgUUID(customerID)
	if err != nil {
		return err
	}

	tag, err := r.db.Exec(ctx,
		`UPDATE users SET customer_id = $2, updated_at = $3 WHERE id = $1`,
		uid, cid, at.UTC(),
	)
	if err != nil {
		return fmt.Errorf("set customer id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("set customer id for user %s: %w", userID, errs.ErrNotFound)
	}
	return nil
}

func scanUser(row pgx.Row) (domain.User, error) {
	var (
		id         pgtype.UUID
		email      string
		password   string
		role       string
		customerID pgtype.UUID
		createdAt  time.Time
		updatedAt  time.Time
	)
	if err := row.Scan(&id, &email, &password, &role, &customerID, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf("user not found: %w", errs.ErrNotFound)
		}
		return domain.User{}, fmt.Errorf("scan user: %w", err)
	}

	return domain.User{
		ID:           fromPgUUID(id),
		Email:        email,
		PasswordHash: password,
		Role:         domain.Role(role),
		CustomerID:   fromPgUUID(customerID),
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}, nil
}
