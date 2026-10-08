package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidProfile = errors.New("invalid profile")

type ProfileFields struct {
	FullName    string
	DateOfBirth string
	Address     string
	Phone       string
	Citizenship string
}

const dateLayout = "2006-01-02"

func (p ProfileFields) Normalize() ProfileFields {
	p.FullName = strings.TrimSpace(p.FullName)
	p.DateOfBirth = strings.TrimSpace(p.DateOfBirth)
	p.Address = strings.TrimSpace(p.Address)
	p.Phone = strings.TrimSpace(p.Phone)
	p.Citizenship = strings.ToUpper(strings.TrimSpace(p.Citizenship))
	return p
}

func ValidateProfile(p ProfileFields, now time.Time) (ProfileFields, error) {
	p = p.Normalize()

	if p.FullName == "" {
		return ProfileFields{}, fmt.Errorf("%w: full_name is required", ErrInvalidProfile)
	}
	if p.Address == "" {
		return ProfileFields{}, fmt.Errorf("%w: address is required", ErrInvalidProfile)
	}
	if p.Phone == "" {
		return ProfileFields{}, fmt.Errorf("%w: phone is required", ErrInvalidProfile)
	}
	if err := validateDateOfBirth(p.DateOfBirth, now); err != nil {
		return ProfileFields{}, err
	}
	if err := validateCitizenship(p.Citizenship); err != nil {
		return ProfileFields{}, err
	}
	return p, nil
}

func validateDateOfBirth(value string, now time.Time) error {
	if value == "" {
		return fmt.Errorf("%w: date_of_birth is required", ErrInvalidProfile)
	}
	dob, err := time.Parse(dateLayout, value)
	if err != nil {
		return fmt.Errorf("%w: date_of_birth %q is not a valid ISO-8601 date", ErrInvalidProfile, value)
	}
	if dob.After(now.UTC()) {
		return fmt.Errorf("%w: date_of_birth %q is in the future", ErrInvalidProfile, value)
	}
	return nil
}

func validateCitizenship(value string) error {
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return fmt.Errorf("%w: citizenship %q is not an ISO-3166 alpha-2 code", ErrInvalidProfile, value)
		}
	}
	if len(value) != 2 {
		return fmt.Errorf("%w: citizenship %q is not an ISO-3166 alpha-2 code", ErrInvalidProfile, value)
	}
	return nil
}
