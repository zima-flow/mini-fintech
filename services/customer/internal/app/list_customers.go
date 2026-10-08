package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

type ListCustomersCommand struct {
	ActorRole domain.Role
	Status    domain.CustomerStatus
	PageSize  int
	PageToken string
}

type ListCustomersResult struct {
	Customers     []domain.Customer
	NextPageToken string
}

type ListCustomersUseCase struct {
	customers domain.CustomerRepo
}

func NewListCustomersUseCase(customers domain.CustomerRepo) *ListCustomersUseCase {
	return &ListCustomersUseCase{customers: customers}
}

func (u *ListCustomersUseCase) ListCustomers(ctx context.Context, cmd ListCustomersCommand) (ListCustomersResult, error) {
	if !cmd.ActorRole.IsStaff() {
		return ListCustomersResult{}, fmt.Errorf("list customers: role %q is not permitted: %w", cmd.ActorRole, errs.ErrPermissionDenied)
	}
	if cmd.Status != "" && !cmd.Status.Valid() {
		return ListCustomersResult{}, fmt.Errorf("list customers: invalid status %q: %w", cmd.Status, errs.ErrInvalidArgument)
	}

	limit, offset, err := resolvePage(cmd.PageSize, cmd.PageToken)
	if err != nil {
		return ListCustomersResult{}, err
	}

	rows, err := u.customers.List(ctx, domain.CustomerFilter{
		Status: cmd.Status,
		Limit:  limit + 1,
		Offset: offset,
	})
	if err != nil {
		return ListCustomersResult{}, fmt.Errorf("list customers: %w", err)
	}

	result := ListCustomersResult{}
	if len(rows) > limit {
		result.NextPageToken = encodePageToken(offset + limit)
		rows = rows[:limit]
	}
	result.Customers = rows
	return result, nil
}

func resolvePage(pageSize int, pageToken string) (limit, offset int, err error) {
	if pageSize < 0 {
		return 0, 0, fmt.Errorf("list customers: page_size must not be negative: %w", errs.ErrInvalidArgument)
	}
	limit = pageSize
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	if pageToken == "" {
		return limit, 0, nil
	}
	raw, decErr := base64.RawURLEncoding.DecodeString(pageToken)
	if decErr != nil {
		return 0, 0, fmt.Errorf("list customers: invalid page token: %w", errs.ErrInvalidArgument)
	}
	offset, convErr := strconv.Atoi(string(raw))
	if convErr != nil || offset < 0 {
		return 0, 0, fmt.Errorf("list customers: invalid page token: %w", errs.ErrInvalidArgument)
	}
	return limit, offset, nil
}

func encodePageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}
