package app

import (
	"context"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type GetCustomerCommand struct {
	CustomerID string
	ActorRole  domain.Role
}

type GetCustomerResult struct {
	Customer domain.Customer
}

type GetCustomerUseCase struct {
	customers domain.CustomerRepo
}

func NewGetCustomerUseCase(customers domain.CustomerRepo) *GetCustomerUseCase {
	return &GetCustomerUseCase{customers: customers}
}

func (u *GetCustomerUseCase) GetCustomer(ctx context.Context, cmd GetCustomerCommand) (GetCustomerResult, error) {
	if !cmd.ActorRole.IsStaff() {
		return GetCustomerResult{}, fmt.Errorf("get customer: role %q is not permitted: %w", cmd.ActorRole, errs.ErrPermissionDenied)
	}
	if cmd.CustomerID == "" {
		return GetCustomerResult{}, fmt.Errorf("get customer: customer_id is required: %w", errs.ErrInvalidArgument)
	}

	customer, err := u.customers.ByID(ctx, cmd.CustomerID)
	if err != nil {
		return GetCustomerResult{}, fmt.Errorf("get customer: %w", err)
	}
	return GetCustomerResult{Customer: customer}, nil
}
