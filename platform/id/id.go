package id

import "github.com/google/uuid"

type Generator interface {
	New() string
}

type UUIDv7 struct{}

func (UUIDv7) New() string {
	u, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return u.String()
}
