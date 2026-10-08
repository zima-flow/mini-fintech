package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func referenceNow() time.Time {
	return time.Date(2001, 2, 3, 10, 0, 0, 0, time.UTC)
}

func validProfile() domain.ProfileFields {
	return domain.ProfileFields{
		FullName:    "Ada Lovelace",
		DateOfBirth: "1990-01-02",
		Address:     "1 Analytical Engine Way",
		Phone:       "+15551234567",
		Citizenship: "GB",
	}
}

func TestValidateProfile_Normalizes(t *testing.T) {
	t.Parallel()

	in := domain.ProfileFields{
		FullName:    "  Ada Lovelace  ",
		DateOfBirth: " 1990-01-02 ",
		Address:     "  1 Analytical Engine Way  ",
		Phone:       "  +15551234567  ",
		Citizenship: " gb ",
	}

	got, err := domain.ValidateProfile(in, referenceNow())
	require.NoError(t, err)
	require.Equal(t, validProfile(), got)
}

func TestValidateProfile_RequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mut  func(*domain.ProfileFields)
	}{
		{name: "full name", mut: func(p *domain.ProfileFields) { p.FullName = "   " }},
		{name: "date of birth", mut: func(p *domain.ProfileFields) { p.DateOfBirth = "" }},
		{name: "address", mut: func(p *domain.ProfileFields) { p.Address = "" }},
		{name: "phone", mut: func(p *domain.ProfileFields) { p.Phone = "" }},
		{name: "citizenship", mut: func(p *domain.ProfileFields) { p.Citizenship = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := validProfile()
			tc.mut(&in)
			_, err := domain.ValidateProfile(in, referenceNow())
			require.ErrorIs(t, err, domain.ErrInvalidProfile)
		})
	}
}

func TestValidateProfile_DateOfBirth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "past date", value: "1990-01-02", ok: true},
		{name: "today", value: "2001-02-03", ok: true},
		{name: "yesterday", value: "2001-02-02", ok: true},
		{name: "future date", value: "2001-02-04", ok: false},
		{name: "wrong separator", value: "1990/01/02", ok: false},
		{name: "american order", value: "02-01-1990", ok: false},
		{name: "invalid month", value: "1990-13-01", ok: false},
		{name: "invalid day", value: "1990-02-30", ok: false},
		{name: "date-time is not a civil date", value: "1990-01-02T00:00:00Z", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := validProfile()
			in.DateOfBirth = tc.value
			_, err := domain.ValidateProfile(in, referenceNow())
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, domain.ErrInvalidProfile)
		})
	}
}

func TestValidateProfile_Citizenship(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "uppercase", value: "GB", want: "GB", ok: true},
		{name: "lowercase canonicalized", value: "us", want: "US", ok: true},
		{name: "one letter", value: "G", ok: false},
		{name: "three letters", value: "GBR", ok: false},
		{name: "digit", value: "G1", ok: false},
		{name: "punctuation", value: "G!", ok: false},
		{name: "non-ascii", value: "ÜS", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := validProfile()
			in.Citizenship = tc.value
			got, err := domain.ValidateProfile(in, referenceNow())
			if !tc.ok {
				require.ErrorIs(t, err, domain.ErrInvalidProfile)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Citizenship)
		})
	}
}
