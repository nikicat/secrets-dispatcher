// Package dhcrypto implements the Secret Service
// "dh-ietf1024-sha256-aes128-cbc-pkcs7" session algorithm.
//
// The algorithm negotiates a shared AES key via Diffie-Hellman over the
// 1024-bit MODP group ("Second Oakley Group") from RFC 2409 §6.2, derives a
// 128-bit AES key from the shared secret with HKDF-SHA256, and transfers
// secret values encrypted with AES-128-CBC using PKCS#7 padding and a random
// per-secret IV.
//
// Both peers of a session run the same steps: GenerateKeyPair to produce an
// ephemeral key pair, exchange the public keys, then Derive to obtain the
// shared Session. This matches the behaviour of libsecret, GNOME Keyring and
// the Python "secretstorage" / Rust "secret-service" client libraries, so the
// resulting sessions interoperate with real Secret Service clients.
package dhcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"

	"golang.org/x/crypto/hkdf"
)

const (
	// modulusLen is the byte length of the 1024-bit DH prime. DH public keys
	// and the shared secret are left-padded to this length before use, as the
	// reference client implementations do.
	modulusLen = 128
	// aesKeyLen is the AES-128 key length (in bytes) produced by HKDF.
	aesKeyLen = 16
)

// dhPrime is the 1024-bit MODP group prime ("Second Oakley Group", RFC 2409
// §6.2), the group mandated by the dh-ietf1024-sha256-aes128-cbc-pkcs7
// algorithm.
var dhPrime, _ = new(big.Int).SetString(
	"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1"+
		"29024E088A67CC74020BBEA63B139B22514A08798E3404DD"+
		"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245"+
		"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED"+
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE65381"+
		"FFFFFFFFFFFFFFFF", 16)

// dhGenerator is the group generator g = 2.
var dhGenerator = big.NewInt(2)

// KeyPair is an ephemeral Diffie-Hellman key pair for a single session.
type KeyPair struct {
	priv *big.Int
	// Public is the DH public key (g^priv mod p) as a big-endian byte slice,
	// left-padded to the modulus length. It is sent to the peer as the
	// OpenSession input (client) or output (service).
	Public []byte
}

// GenerateKeyPair creates a new ephemeral Diffie-Hellman key pair.
func GenerateKeyPair() (*KeyPair, error) {
	// Choose a private exponent uniformly in [2, p-1]. rand.Int returns a value
	// in [0, p-3]; shifting by 2 avoids the degenerate exponents 0 and 1.
	span := new(big.Int).Sub(dhPrime, big.NewInt(2))
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return nil, fmt.Errorf("generate DH private key: %w", err)
	}
	priv := n.Add(n, big.NewInt(2))
	return newKeyPair(priv), nil
}

// newKeyPair builds a KeyPair from a private exponent. Split out so tests can
// supply a deterministic exponent.
func newKeyPair(priv *big.Int) *KeyPair {
	pub := new(big.Int).Exp(dhGenerator, priv, dhPrime)
	return &KeyPair{priv: priv, Public: leftPad(pub.Bytes(), modulusLen)}
}

// Derive completes the key agreement against the peer's public key and returns
// the negotiated Session.
func (kp *KeyPair) Derive(peerPublic []byte) (*Session, error) {
	peer := new(big.Int).SetBytes(peerPublic)
	// Reject degenerate public keys (0, 1, p-1, >= p). These would yield a
	// shared secret in a tiny subgroup and so leak the session key.
	if peer.Cmp(big.NewInt(2)) < 0 || peer.Cmp(new(big.Int).Sub(dhPrime, big.NewInt(1))) >= 0 {
		return nil, errors.New("dhcrypto: peer public key out of range")
	}

	shared := new(big.Int).Exp(peer, kp.priv, dhPrime)
	ikm := leftPad(shared.Bytes(), modulusLen)

	// HKDF-SHA256 with an empty salt (a hash-length run of zero bytes per
	// RFC 5869) and empty info, producing a 128-bit AES key. This matches the
	// derivation used by libsecret and the reference client libraries.
	r := hkdf.New(sha256.New, ikm, nil, nil)
	key := make([]byte, aesKeyLen)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("dhcrypto: derive key: %w", err)
	}
	return &Session{key: key}, nil
}

// Session holds the AES-128 key negotiated for a DH session.
type Session struct {
	key []byte
}

// Encrypt encrypts plaintext with AES-128-CBC and PKCS#7 padding under a fresh
// random IV. It returns the ciphertext and the IV; the IV is transferred to the
// peer in the Secret's Parameters field.
func (s *Session) Encrypt(plaintext []byte) (ciphertext, iv []byte, err error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	iv = make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, nil, fmt.Errorf("dhcrypto: generate IV: %w", err)
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext = make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return ciphertext, iv, nil
}

// Decrypt reverses Encrypt: it decrypts an AES-128-CBC ciphertext under the
// given IV and strips the PKCS#7 padding.
func (s *Session) Decrypt(iv, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("dhcrypto: invalid IV length %d", len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("dhcrypto: invalid ciphertext length %d", len(ciphertext))
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, aes.BlockSize)
}

// leftPad returns b left-padded with zero bytes to size. If b is already at
// least size bytes long it is returned unchanged.
func leftPad(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

// pkcs7Pad appends PKCS#7 padding so the result is a whole number of blocks.
// A full block of padding is added when the input is already block-aligned.
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// pkcs7Unpad removes PKCS#7 padding, validating that it is well-formed.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("dhcrypto: invalid padded data length")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize {
		return nil, errors.New("dhcrypto: invalid PKCS#7 padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("dhcrypto: invalid PKCS#7 padding")
		}
	}
	return data[:len(data)-pad], nil
}
