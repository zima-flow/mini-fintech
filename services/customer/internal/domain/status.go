package domain

import (
	"errors"
	"fmt"
)

type CustomerStatus string

const (
	StatusNew           CustomerStatus = "NEW"
	StatusProfileFilled CustomerStatus = "PROFILE_FILLED"
	StatusOnKYC         CustomerStatus = "ON_KYC"
	StatusActive        CustomerStatus = "ACTIVE"
	StatusRejected      CustomerStatus = "REJECTED"
	StatusBlocked       CustomerStatus = "BLOCKED"
)

var ErrFailedPrecondition = errors.New("failed precondition")

func (s CustomerStatus) Valid() bool {
	switch s {
	case StatusNew, StatusProfileFilled, StatusOnKYC, StatusActive, StatusRejected, StatusBlocked:
		return true
	default:
		return false
	}
}

func CanEditProfile(status CustomerStatus) bool {
	switch status {
	case StatusNew, StatusProfileFilled:
		return true
	default:
		return false
	}
}

func ApplyTransition(from, to CustomerStatus) error {
	if !transitionAllowed(from, to) {
		return fmt.Errorf("transition %q -> %q not allowed: %w", from, to, ErrFailedPrecondition)
	}
	return nil
}

func transitionAllowed(from, to CustomerStatus) bool {
	switch from {
	case StatusNew:
		return to == StatusProfileFilled
	case StatusProfileFilled:
		return to == StatusProfileFilled || to == StatusOnKYC
	case StatusOnKYC:
		return to == StatusActive || to == StatusRejected || to == StatusBlocked
	case StatusActive:
		return to == StatusBlocked
	default:
		return false
	}
}
