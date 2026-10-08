package app

import (
	"context"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type GetProfileCommand struct {
	UserID string
}

type GetProfileResult struct {
	Customer domain.Customer
}

type GetProfileUseCase struct {
	customers domain.CustomerRepo
}

func NewGetProfileUseCase(customers domain.CustomerRepo) *GetProfileUseCase {
	return &GetProfileUseCase{customers: customers}
}

func (u *GetProfileUseCase) GetProfile(ctx context.Context, cmd GetProfileCommand) (GetProfileResult, error) {
	if cmd.UserID == "" {
		return GetProfileResult{}, fmt.Errorf("get profile: user_id is required: %w", errs.ErrInvalidArgument)
	}

	customer, err := u.customers.ByUserID(ctx, cmd.UserID)
	if err != nil {
		return GetProfileResult{}, fmt.Errorf("get profile: %w", err)
	}
	return GetProfileResult{Customer: customer}, nil
}
