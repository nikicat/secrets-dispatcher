package dhcrypto

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
)

// mustHex decodes a hex string or fails the test.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return b
}

// keyPairFromHex builds a KeyPair from a fixed private exponent (hex). Used to
// reproduce deterministic golden vectors.
func keyPairFromHex(t *testing.T, privHex string) *KeyPair {
	t.Helper()
	priv, ok := new(big.Int).SetString(privHex, 16)
	if !ok {
		t.Fatalf("parse priv hex %q", privHex)
	}
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

	if got := hex.EncodeToString(client.Public); got != clientPubHex {
		t.Errorf("client public key:\n got %s\nwant %s", got, clientPubHex)
	}
	if got := hex.EncodeToString(server.Public); got != serverPubHex {
		t.Errorf("server public key:\n got %s\nwant %s", got, serverPubHex)
	}

	wantKey := mustHex(t, aesKeyHex)
	clientSession, err := client.Derive(server.Public)
	if err != nil {
		t.Fatalf("client derive: %v", err)
	}
	if !bytes.Equal(clientSession.key, wantKey) {
		t.Errorf("client-derived key:\n got %x\nwant %x", clientSession.key, wantKey)
	}
	serverSession, err := server.Derive(client.Public)
	if err != nil {
		t.Fatalf("server derive: %v", err)
	}
	if !bytes.Equal(serverSession.key, wantKey) {
		t.Errorf("server-derived key:\n got %x\nwant %x", serverSession.key, wantKey)
	}
}

// TestKeyAgreement checks that two freshly generated key pairs derive the same
// session key from each other's public keys, and that the public keys are the
// full modulus length.
func TestKeyAgreement(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair a: %v", err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair b: %v", err)
	}
	if len(a.Public) != modulusLen || len(b.Public) != modulusLen {
		t.Fatalf("public keys not padded to %d bytes: %d, %d", modulusLen, len(a.Public), len(b.Public))
	}

	sa, err := a.Derive(b.Public)
	if err != nil {
		t.Fatalf("a.Derive: %v", err)
	}
	sb, err := b.Derive(a.Public)
	if err != nil {
		t.Fatalf("b.Derive: %v", err)
	}
	if !bytes.Equal(sa.key, sb.key) {
		t.Errorf("session keys differ: %x vs %x", sa.key, sb.key)
	}
	if len(sa.key) != aesKeyLen {
		t.Errorf("AES key length = %d, want %d", len(sa.key), aesKeyLen)
	}
}

// TestEncryptDecryptRoundTrip exercises encryption across a range of plaintext
// lengths, including empty and block-aligned inputs (full padding block).
func TestEncryptDecryptRoundTrip(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	enc, err := a.Derive(b.Public)
	if err != nil {
		t.Fatalf("derive enc: %v", err)
	}
	dec, err := b.Derive(a.Public)
	if err != nil {
		t.Fatalf("derive dec: %v", err)
	}

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
		if err != nil {
			t.Fatalf("case %d: encrypt: %v", i, err)
		}
		if len(iv) != 16 {
			t.Errorf("case %d: IV length = %d, want 16", i, len(iv))
		}
		if len(ct) == 0 || len(ct)%16 != 0 {
			t.Errorf("case %d: ciphertext length = %d, want non-zero multiple of 16", i, len(ct))
		}
		if bytes.Equal(ct, pt) && len(pt) > 0 {
			t.Errorf("case %d: ciphertext equals plaintext", i)
		}
		got, err := dec.Decrypt(iv, ct)
		if err != nil {
			t.Fatalf("case %d: decrypt: %v", i, err)
		}
		if !bytes.Equal(got, pt) && !(len(got) == 0 && len(pt) == 0) {
			t.Errorf("case %d: round-trip mismatch: got %q want %q", i, got, pt)
		}
	}
}

// TestEncryptFreshIV verifies each Encrypt call uses a fresh IV, so encrypting
// the same plaintext twice yields different ciphertexts.
func TestEncryptFreshIV(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	s, err := a.Derive(b.Public)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	pt := []byte("repeat me")
	ct1, iv1, _ := s.Encrypt(pt)
	ct2, iv2, _ := s.Encrypt(pt)
	if bytes.Equal(iv1, iv2) {
		t.Error("IV reused across Encrypt calls")
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("ciphertext identical across Encrypt calls (IV not applied)")
	}
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
		if _, err := kp.Derive(pub); err == nil {
			t.Errorf("case %d: expected error for degenerate peer key", i)
		}
	}
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	s, err := a.Derive(b.Public)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	ct, iv, _ := s.Encrypt([]byte("secret"))

	if _, err := s.Decrypt(iv[:8], ct); err == nil {
		t.Error("expected error for short IV")
	}
	if _, err := s.Decrypt(iv, ct[:len(ct)-1]); err == nil {
		t.Error("expected error for non-block-aligned ciphertext")
	}
	if _, err := s.Decrypt(iv, nil); err == nil {
		t.Error("expected error for empty ciphertext")
	}
	// Corrupt the last block so the PKCS#7 padding is (almost certainly) invalid.
	corrupt := bytes.Clone(ct)
	corrupt[len(corrupt)-1] ^= 0xFF
	if _, err := s.Decrypt(iv, corrupt); err == nil {
		t.Error("expected padding error for corrupted ciphertext")
	}
}
