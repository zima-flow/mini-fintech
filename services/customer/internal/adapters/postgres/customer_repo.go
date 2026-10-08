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
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

const customerColumns = `id, user_id, full_name, date_of_birth::text, address, phone, citizenship::text, status, created_at, updated_at`

type CustomerRepo struct {
	db platformpostgres.DBTX
}

var _ domain.CustomerRepo = (*CustomerRepo)(nil)

func NewCustomerRepo(db platformpostgres.DBTX) *CustomerRepo { return &CustomerRepo{db: db} }

func (r *CustomerRepo) Create(ctx context.Context, customer domain.Customer) error {
	id, err := toPgUUID(customer.ID)
	if err != nil {
		return err
	}
	userID, err := toPgUUID(customer.UserID)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx,
		`INSERT INTO customers (id, user_id, full_name, date_of_birth, address, phone, citizenship, status, created_at, updated_at)
		 VALUES ($1, $2, $3, NULLIF($4, '')::date, $5, $6, NULLIF($7, '')::char(2), $8, $9, $10)`,
		id, userID, customer.FullName, customer.DateOfBirth, customer.Address, customer.Phone,
		customer.Citizenship, string(customer.Status), customer.CreatedAt.UTC(), customer.UpdatedAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("insert customer for user %s: %w", customer.UserID, errs.ErrAlreadyExists)
		}
		return fmt.Errorf("insert customer: %w", err)
	}
	return nil
}

func (r *CustomerRepo) ByUserID(ctx context.Context, userID string) (domain.Customer, error) {
	uid, err := toPgUUID(userID)
	if err != nil {
		return domain.Customer{}, err
	}
	return scanCustomer(r.db.QueryRow(ctx, `SELECT `+customerColumns+` FROM customers WHERE user_id = $1`, uid))
}

func (r *CustomerRepo) ByID(ctx context.Context, customerID string) (domain.Customer, error) {
	cid, err := toPgUUID(customerID)
	if err != nil {
		return domain.Customer{}, err
	}
	return scanCustomer(r.db.QueryRow(ctx, `SELECT `+customerColumns+` FROM customers WHERE id = $1`, cid))
}

func (r *CustomerRepo) Update(ctx context.Context, customer domain.Customer) error {
	cid, err := toPgUUID(customer.ID)
	if err != nil {
		return err
	}

	tag, err := r.db.Exec(ctx,
		`UPDATE customers
		    SET full_name = $2, date_of_birth = NULLIF($3, '')::date, address = $4,
		        phone = $5, citizenship = NULLIF($6, '')::char(2), status = $7, updated_at = $8
		  WHERE id = $1`,
		cid, customer.FullName, customer.DateOfBirth, customer.Address, customer.Phone,
		customer.Citizenship, string(customer.Status), customer.UpdatedAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("update customer %s: %w", customer.ID, errs.ErrAlreadyExists)
		}
		return fmt.Errorf("update customer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update customer %s: %w", customer.ID, errs.ErrNotFound)
	}
	return nil
}

func (r *CustomerRepo) List(ctx context.Context, filter domain.CustomerFilter) ([]domain.Customer, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+customerColumns+` FROM customers
		  WHERE ($1 = '' OR status = $1)
		  ORDER BY created_at, id
		  LIMIT $2 OFFSET $3`,
		string(filter.Status), filter.Limit, filter.Offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	defer rows.Close()

	var customers []domain.Customer
	for rows.Next() {
		customer, scanErr := scanCustomer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		customers = append(customers, customer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read customers: %w", err)
	}
	return customers, nil
}

func scanCustomer(row pgx.Row) (domain.Customer, error) {
	var (
		id          pgtype.UUID
		userID      pgtype.UUID
		fullName    string
		dateOfBirth *string
		address     string
		phone       string
		citizenship *string
		status      string
		createdAt   time.Time
		updatedAt   time.Time
	)
	if err := row.Scan(&id, &userID, &fullName, &dateOfBirth, &address, &phone, &citizenship,
		&status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Customer{}, fmt.Errorf("customer not found: %w", errs.ErrNotFound)
		}
		return domain.Customer{}, fmt.Errorf("scan customer: %w", err)
	}

	return domain.Customer{
		ID:          fromPgUUID(id),
		UserID:      fromPgUUID(userID),
		FullName:    fullName,
		DateOfBirth: derefString(dateOfBirth),
		Address:     address,
		Phone:       phone,
		Citizenship: derefString(citizenship),
		Status:      domain.CustomerStatus(status),
		CreatedAt:   createdAt.UTC(),
		UpdatedAt:   updatedAt.UTC(),
	}, nil
}
