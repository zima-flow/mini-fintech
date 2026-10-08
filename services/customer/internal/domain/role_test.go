package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestRole_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role domain.Role
		want bool
	}{
		{name: "client", role: domain.RoleClient, want: true},
		{name: "officer", role: domain.RoleOfficer, want: true},
		{name: "admin", role: domain.RoleAdmin, want: true},
		{name: "empty", role: domain.Role(""), want: false},
		{name: "unknown", role: domain.Role("AUDITOR"), want: false},
		{name: "lowercase is not canonical", role: domain.Role("officer"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.role.Valid())
		})
	}
}

func TestRole_IsStaff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role domain.Role
		want bool
	}{
		{name: "client is not staff", role: domain.RoleClient, want: false},
		{name: "officer is staff", role: domain.RoleOfficer, want: true},
		{name: "admin is staff", role: domain.RoleAdmin, want: true},
		{name: "empty is not staff", role: domain.Role(""), want: false},
		{name: "unknown is not staff", role: domain.Role("AUDITOR"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.role.IsStaff())
		})
	}
}
