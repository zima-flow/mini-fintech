package domain

import "time"

type Customer struct {
	ID          string
	UserID      string
	FullName    string
	DateOfBirth string
	Address     string
	Phone       string
	Citizenship string
	Status      CustomerStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
