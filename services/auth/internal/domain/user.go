package domain

import (
	"errors"
	"strings"
	"time"
)

type Role string

const (
	RoleClient  Role = "CLIENT"
	RoleOfficer Role = "OFFICER"
	RoleAdmin   Role = "ADMIN"
)

func (r Role) Valid() bool {
	switch r {
	case RoleClient, RoleOfficer, RoleAdmin:
		return true
	default:
		return false
	}
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         Role
	CustomerID   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

const maxEmailLength = 254

var (
	ErrInvalidEmail    = errors.New("invalid email")
	ErrInvalidPassword = errors.New("invalid password")
)

func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func ValidateEmail(email string) error {
	if email == "" || len(email) > maxEmailLength {
		return ErrInvalidEmail
	}

	at := strings.IndexByte(email, '@')
	if at < 0 || at != strings.LastIndexByte(email, '@') {
		return ErrInvalidEmail
	}
	if err := validateLocalPart(email[:at]); err != nil {
		return err
	}
	return validateDomain(email[at+1:])
}

func validateLocalPart(local string) error {
	if local == "" || len(local) > 64 {
		return ErrInvalidEmail
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return ErrInvalidEmail
	}
	for _, r := range local {
		if !isLocalRune(r) {
			return ErrInvalidEmail
		}
	}
	return nil
}

func validateDomain(domain string) error {
	if domain == "" || len(domain) > 255 || !strings.Contains(domain, ".") {
		return ErrInvalidEmail
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return ErrInvalidEmail
	}
	for _, label := range strings.Split(domain, ".") {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateLabel(label string) error {
	if label == "" || len(label) > 63 {
		return ErrInvalidEmail
	}
	for i, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(label)-1 {
				return ErrInvalidEmail
			}
		default:
			return ErrInvalidEmail
		}
	}
	return nil
}

func isLocalRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '.', '!', '#', '$', '%', '&', '\'', '*', '+', '-', '/', '=', '?', '^', '_', '`', '{', '|', '}', '~':
		return true
	default:
		return false
	}
}

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrInvalidPassword
	}
	return nil
}
