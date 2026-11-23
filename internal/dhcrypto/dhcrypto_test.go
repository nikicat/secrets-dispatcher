package dhcrypto

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustHex decodes a hex string or fails the test.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// keyPairFromHex builds a KeyPair from a fixed private exponent (hex). Used to
// reproduce deterministic golden vectors.
func keyPairFromHex(t *testing.T, privHex string) *KeyPair {
	t.Helper()
	priv, ok := new(big.Int).SetString(privHex, 16)
	require.True(t, ok, "parse priv hex %q", privHex)
	return newKeyPair(priv)
}

// TestGoldenVector pins the DH public keys and the derived AES key against an
// independent reference implementation (Python: native bigint modexp + a
// hand-rolled HKDF-SHA256). A mismatch here means the proxy would fail to
// interoperate with real Secret Service clients (libsecret, secretstorage).
func TestGoldenVector(t *testing.T) {
	const (
		clientPrivHex = "fedcba987654321fedcba9876543210abcdef0123456789abcdef0123456789a"
		serverPrivHex = "112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		clientPubHex  = "da8f30c8c3615a288f12bb27c05a31f32c687ffe5d8a6af8083a8f18d63caa31" +
			"4ff6f4995ad00c870da3e9de1ee78f43051f1d7bdade69dc58a915b726509ea5" +
			"6ba885a34733e1c67c1d3e970953e1c34b4b1c7ca5d42df9164050bc1e8c58ea" +
			"dbdd4ccac298a9378b6b12d669969439285c1737bc672a230f93208a38fb47ac"
		serverPubHex = "6b24f4786422f0a9128efbc808d6d737c6146f160030a328662368feaddc6c15" +
			"f5fe296b183425a2320f656d75360659e58707275a975374d4764d4ba8783f6e" +
			"42a7c1e50609074245ecbe89255aa84ff15be5bbd04c708244db1b37de62571a" +
			"b13d5228d090f304ad6aeebf8cfc98f6befe62fc64bd0051189b7ac95a052b35"
		aesKeyHex = "19e02dd39f0630467cd0c8421a80c0e4"
	)

	client := keyPairFromHex(t, clientPrivHex)
	server := keyPairFromHex(t, serverPrivHex)

	assert.Equal(t, clientPubHex, hex.EncodeToString(client.Public), "client public key")
	assert.Equal(t, serverPubHex, hex.EncodeToString(server.Public), "server public key")

	wantKey := mustHex(t, aesKeyHex)
	clientSession, err := client.Derive(server.Public)
	require.NoError(t, err, "client derive")
	assert.Equal(t, wantKey, clientSession.key, "client-derived key")
	serverSession, err := server.Derive(client.Public)
	require.NoError(t, err, "server derive")
	assert.Equal(t, wantKey, serverSession.key, "server-derived key")
}

// Returns the two ends of a session between two fresh key pairs.
func sessionPair(t *testing.T) (enc, dec *Session) {
	t.Helper()
	a, err := GenerateKeyPair()
	require.NoError(t, err)
	b, err := GenerateKeyPair()
	require.NoError(t, err)
	enc, err = a.Derive(b.Public)
	require.NoError(t, err)
	dec, err = b.Derive(a.Public)
	require.NoError(t, err)
	return enc, dec
}

// TestKeyAgreement checks that two freshly generated key pairs derive the same
// session key from each other's public keys, and that the public keys are the
// full modulus length.
func TestKeyAgreement(t *testing.T) {
	a, err := GenerateKeyPair()
	require.NoError(t, err, "GenerateKeyPair a")
	b, err := GenerateKeyPair()
	require.NoError(t, err, "GenerateKeyPair b")
	require.Len(t, a.Public, modulusLen, "public key a not padded")
	require.Len(t, b.Public, modulusLen, "public key b not padded")

	sa, err := a.Derive(b.Public)
	require.NoError(t, err, "a.Derive")
	sb, err := b.Derive(a.Public)
	require.NoError(t, err, "b.Derive")
	assert.Equal(t, sa.key, sb.key, "session keys differ")
	assert.Len(t, sa.key, aesKeyLen)
}

// TestEncryptDecryptRoundTrip exercises encryption across a range of plaintext
// lengths, including empty and block-aligned inputs (full padding block).
func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, dec := sessionPair(t)

	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("x"),
		[]byte("hunter2"),
		bytes.Repeat([]byte("A"), 16), // exactly one block
		bytes.Repeat([]byte("B"), 17),
		bytes.Repeat([]byte{0}, 64),
		[]byte("a longer multi-block secret value that spans several AES blocks!!"),
	}
	for i, pt := range cases {
		ct, iv, err := enc.Encrypt(pt)
		require.NoError(t, err, "case %d: encrypt", i)
		assert.Len(t, iv, 16, "case %d: IV length", i)
		assert.True(t, len(ct) > 0 && len(ct)%16 == 0,
			"case %d: ciphertext length = %d, want non-zero multiple of 16", i, len(ct))
		if len(pt) > 0 {
			assert.NotEqual(t, pt, ct, "case %d: ciphertext equals plaintext", i)
		}
		got, err := dec.Decrypt(iv, ct)
		require.NoError(t, err, "case %d: decrypt", i)
		assert.Equal(t, string(pt), string(got), "case %d: round-trip mismatch", i)
	}
}

// TestEncryptFreshIV verifies each Encrypt call uses a fresh IV, so encrypting
// the same plaintext twice yields different ciphertexts.
func TestEncryptFreshIV(t *testing.T) {
	s, _ := sessionPair(t)
	pt := []byte("repeat me")
	ct1, iv1, _ := s.Encrypt(pt)
	ct2, iv2, _ := s.Encrypt(pt)
	assert.NotEqual(t, iv1, iv2, "IV reused across Encrypt calls")
	assert.NotEqual(t, ct1, ct2, "ciphertext identical across Encrypt calls (IV not applied)")
}

func TestDeriveRejectsBadPeerKey(t *testing.T) {
	kp, _ := GenerateKeyPair()
	bad := [][]byte{
		nil,             // 0
		{0x00},          // 0
		{0x01},          // 1
		dhPrime.Bytes(), // p itself (>= p)
	}
	for i, pub := range bad {
		_, err := kp.Derive(pub)
		assert.Error(t, err, "case %d: degenerate peer key", i)
	}
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	s, _ := sessionPair(t)
	ct, iv, _ := s.Encrypt([]byte("secret"))

	_, err := s.Decrypt(iv[:8], ct)
	assert.Error(t, err, "short IV")
	_, err = s.Decrypt(iv, ct[:len(ct)-1])
	assert.Error(t, err, "non-block-aligned ciphertext")
	_, err = s.Decrypt(iv, nil)
	assert.Error(t, err, "empty ciphertext")
	// Corrupt the last block so the PKCS#7 padding is (almost certainly) invalid.
	corrupt := bytes.Clone(ct)
	corrupt[len(corrupt)-1] ^= 0xFF
	_, err = s.Decrypt(iv, corrupt)
	assert.Error(t, err, "corrupted ciphertext padding")
}
