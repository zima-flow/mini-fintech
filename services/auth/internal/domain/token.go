package domain

import "time"

type AccessClaims struct {
	UserID     string
	Role       Role
	CustomerID string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	TokenID    string // jti
}

type TokenPair struct {
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
}
