package app

import (
	"context"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type GetCustomerStatusCommand struct {
	UserID     string
	CustomerID string
	ActorRole  domain.Role
}

type GetCustomerStatusResult struct {
	CustomerID string
	Status     domain.CustomerStatus
}

type GetCustomerStatusUseCase struct {
	customers domain.CustomerRepo
}

func NewGetCustomerStatusUseCase(customers domain.CustomerRepo) *GetCustomerStatusUseCase {
	return &GetCustomerStatusUseCase{customers: customers}
}

func (u *GetCustomerStatusUseCase) GetCustomerStatus(ctx context.Context, cmd GetCustomerStatusCommand) (GetCustomerStatusResult, error) {
	if !cmd.ActorRole.Valid() {
		return GetCustomerStatusResult{}, fmt.Errorf("get customer status: invalid caller role %q: %w", cmd.ActorRole, errs.ErrPermissionDenied)
	}

	var customer domain.Customer
	var err error
	if cmd.ActorRole.IsStaff() {
		if cmd.CustomerID == "" {
			return GetCustomerStatusResult{}, fmt.Errorf("get customer status: customer_id is required for staff: %w", errs.ErrInvalidArgument)
		}
		customer, err = u.customers.ByID(ctx, cmd.CustomerID)
	} else {
		if cmd.UserID == "" {
			return GetCustomerStatusResult{}, fmt.Errorf("get customer status: user_id is required: %w", errs.ErrInvalidArgument)
		}
		customer, err = u.customers.ByUserID(ctx, cmd.UserID)
	}
	if err != nil {
		return GetCustomerStatusResult{}, fmt.Errorf("get customer status: %w", err)
	}
	return GetCustomerStatusResult{CustomerID: customer.ID, Status: customer.Status}, nil
}
