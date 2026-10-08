package domain

import (
	"crypto/sha256"
	"time"
)

func HashRefreshToken(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash []byte
	FamilyID  string
	IssuedAt  time.Time
	ExpiresAt time.Time

	RotatedAt    *time.Time
	ReplacedByID string

	RevokedAt     *time.Time
	RevokedReason string
}

func (t RefreshToken) Rotated() bool { return t.RotatedAt != nil }

func (t RefreshToken) Revoked() bool { return t.RevokedAt != nil }

func (t RefreshToken) Expired(now time.Time) bool { return !now.Before(t.ExpiresAt) }

func (t RefreshToken) Reusable(now time.Time) bool {
	return !t.Rotated() && !t.Revoked() && !t.Expired(now)
}

type TokenFamily struct {
	ID     string
	UserID string
}
