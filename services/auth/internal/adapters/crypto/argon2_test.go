package crypto_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	passwordcrypto "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/crypto"
)

const password = "correct horse battery staple"

func TestHasher_HashVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	h := passwordcrypto.NewHasher()

	encoded, err := h.Hash(password)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, "$argon2id$"), "PHC string must declare argon2id")

	ok, err := h.Verify(password, encoded)
	require.NoError(t, err)
	require.True(t, ok)

	wrong, err := h.Verify("not the password", encoded)
	require.NoError(t, err)
	require.False(t, wrong, "a wrong password must not verify")
}

func TestHasher_HashIsSalted(t *testing.T) {
	t.Parallel()

	h := passwordcrypto.NewHasher()

	first, err := h.Hash(password)
	require.NoError(t, err)
	second, err := h.Hash(password)
	require.NoError(t, err)

	require.NotEqual(t, first, second, "each hash must use a fresh salt")

	for _, encoded := range []string{first, second} {
		ok, err := h.Verify(password, encoded)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

func TestHasher_VerifyMalformed(t *testing.T) {
	t.Parallel()

	h := passwordcrypto.NewHasher()

	tests := []struct {
		name    string
		encoded string
	}{
		{name: "empty", encoded: ""},
		{name: "not a phc string", encoded: "hello"},
		{name: "wrong scheme", encoded: "$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$a2V5"},
		{name: "missing part", encoded: "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA"},
		{name: "wrong version", encoded: "$argon2id$v=16$m=65536,t=3,p=4$c2FsdA$a2V5"},
		{name: "bad base64 salt", encoded: "$argon2id$v=19$m=65536,t=3,p=4$!!!$a2V5"},
		{name: "empty salt", encoded: "$argon2id$v=19$m=65536,t=3,p=4$$a2V5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ok, err := h.Verify(password, tc.encoded)
			require.Error(t, err)
			require.False(t, ok)
		})
	}
}
