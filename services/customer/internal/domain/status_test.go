package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func allStatuses() []domain.CustomerStatus {
	return []domain.CustomerStatus{
		domain.StatusNew,
		domain.StatusProfileFilled,
		domain.StatusOnKYC,
		domain.StatusActive,
		domain.StatusRejected,
		domain.StatusBlocked,
	}
}

func TestCustomerStatus_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.CustomerStatus
		want   bool
	}{
		{name: "new", status: domain.StatusNew, want: true},
		{name: "profile filled", status: domain.StatusProfileFilled, want: true},
		{name: "on kyc", status: domain.StatusOnKYC, want: true},
		{name: "active", status: domain.StatusActive, want: true},
		{name: "rejected", status: domain.StatusRejected, want: true},
		{name: "blocked", status: domain.StatusBlocked, want: true},
		{name: "empty", status: domain.CustomerStatus(""), want: false},
		{name: "unknown", status: domain.CustomerStatus("ARCHIVED"), want: false},
		{name: "proto unspecified name", status: domain.CustomerStatus("CUSTOMER_STATUS_UNSPECIFIED"), want: false},
		{name: "lowercase is not canonical", status: domain.CustomerStatus("new"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.status.Valid())
		})
	}
}

func TestApplyTransition_Exhaustive(t *testing.T) {
	t.Parallel()

	allowed := map[domain.CustomerStatus]map[domain.CustomerStatus]bool{
		domain.StatusNew: {
			domain.StatusProfileFilled: true,
		},
		domain.StatusProfileFilled: {
			domain.StatusProfileFilled: true,
			domain.StatusOnKYC:         true,
		},
		domain.StatusOnKYC: {
			domain.StatusActive:   true,
			domain.StatusRejected: true,
			domain.StatusBlocked:  true,
		},
		domain.StatusActive: {
			domain.StatusBlocked: true,
		},
	}

	for _, from := range allStatuses() {
		for _, to := range allStatuses() {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				t.Parallel()

				err := domain.ApplyTransition(from, to)
				if allowed[from][to] {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, domain.ErrFailedPrecondition)
			})
		}
	}
}

func TestApplyTransition_UnknownStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from domain.CustomerStatus
		to   domain.CustomerStatus
	}{
		{name: "unknown from", from: domain.CustomerStatus("ARCHIVED"), to: domain.StatusProfileFilled},
		{name: "unknown to", from: domain.StatusNew, to: domain.CustomerStatus("ARCHIVED")},
		{name: "empty from", from: domain.CustomerStatus(""), to: domain.StatusNew},
		{name: "empty to", from: domain.StatusNew, to: domain.CustomerStatus("")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, domain.ApplyTransition(tc.from, tc.to), domain.ErrFailedPrecondition)
		})
	}
}

func TestCanEditProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.CustomerStatus
		want   bool
	}{
		{name: "new is editable", status: domain.StatusNew, want: true},
		{name: "profile filled is editable", status: domain.StatusProfileFilled, want: true},
		{name: "on kyc is locked", status: domain.StatusOnKYC, want: false},
		{name: "active is locked", status: domain.StatusActive, want: false},
		{name: "rejected is locked", status: domain.StatusRejected, want: false},
		{name: "blocked is locked", status: domain.StatusBlocked, want: false},
		{name: "empty is locked", status: domain.CustomerStatus(""), want: false},
		{name: "unknown is locked", status: domain.CustomerStatus("ARCHIVED"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, domain.CanEditProfile(tc.status))
		})
	}
}
