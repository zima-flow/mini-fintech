package id_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

func TestUUIDv7New_ReturnsVersion7(t *testing.T) {
	t.Parallel()
	gen := id.UUIDv7{}

	u, err := uuid.Parse(gen.New())

	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), u.Version())
}

func TestUUIDv7New_IsUnique(t *testing.T) {
	t.Parallel()
	gen := id.UUIDv7{}
	require.NotEqual(t, gen.New(), gen.New())
}
