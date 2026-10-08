package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "trims and lowercases", in: "  User@Example.COM  ", want: "user@example.com"},
		{name: "already normalized is unchanged", in: "user@example.com", want: "user@example.com"},
		{name: "trims tabs and newlines", in: "\tJane.Doe@Bank.IE\n", want: "jane.doe@bank.ie"},
		{name: "empty stays empty", in: "", want: ""},
		{name: "only whitespace becomes empty", in: "   ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, domain.NormalizeEmail(tc.in))
		})
	}
}

func TestValidateEmail_Accepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
	}{
		{name: "simple", email: "user@example.com"},
		{name: "plus tag and subdomain", email: "first.last+tag@sub.example.co.uk"},
		{name: "short domain", email: "a@b.io"},
		{name: "digits", email: "user123@example123.com"},
		{name: "hyphenated label", email: "user@my-bank.example.com"},
		{name: "uppercase is structurally valid", email: "User@Example.COM"},
		{name: "local part of maximum length", email: strings.Repeat("a", 64) + "@example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, domain.ValidateEmail(tc.email))
		})
	}
}

func TestValidateEmail_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
	}{
		{name: "empty", email: ""},
		{name: "no at sign", email: "userexample.com"},
		{name: "missing local part", email: "@example.com"},
		{name: "missing domain", email: "user@"},
		{name: "two at signs adjacent", email: "user@@example.com"},
		{name: "two at signs apart", email: "us@er@example.com"},
		{name: "domain without a dot", email: "user@localhost"},
		{name: "leading dot in local part", email: ".user@example.com"},
		{name: "trailing dot in local part", email: "user.@example.com"},
		{name: "consecutive dots in local part", email: "us..er@example.com"},
		{name: "leading dot in domain", email: "user@.example.com"},
		{name: "trailing dot in domain", email: "user@example.com."},
		{name: "consecutive dots in domain", email: "user@example..com"},
		{name: "space in local part", email: "us er@example.com"},
		{name: "space in domain", email: "user@exa mple.com"},
		{name: "label starts with hyphen", email: "user@-example.com"},
		{name: "label ends with hyphen", email: "user@example-.com"},
		{name: "local part too long", email: strings.Repeat("a", 65) + "@example.com"},
		{name: "domain label too long", email: "user@" + strings.Repeat("b", 64) + ".com"},
		{
			name: "address too long",
			email: strings.Repeat("a", 64) + "@" +
				strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." +
				strings.Repeat("d", 63) + ".com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, domain.ValidateEmail(tc.email), domain.ErrInvalidEmail)
		})
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  int
		wantErr bool
	}{
		{name: "empty", policy: 0, wantErr: true},
		{name: "below minimum", policy: 11, wantErr: true},
		{name: "at minimum", policy: 12, wantErr: false},
		{name: "at maximum", policy: 128, wantErr: false},
		{name: "above maximum", policy: 129, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := domain.ValidatePassword(strings.Repeat("a", tc.policy))
			if tc.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidPassword)
				return
			}
			require.NoError(t, err)
		})
	}
}

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
		{name: "unknown", role: domain.Role("SUPERUSER"), want: false},
		{name: "case sensitive", role: domain.Role("client"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.role.Valid())
		})
	}
}
