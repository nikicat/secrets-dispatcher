package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	jwtExpiry = 5 * time.Minute
)

// jwtHeader is the standard JWT header for HS256.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// jwtClaims contains the JWT claims we use.
type jwtClaims struct {
	Exp int64 `json:"exp"`
	Iat int64 `json:"iat"`
}

// GenerateJWT creates a short-lived JWT signed with the auth token.
func (a *Auth) GenerateJWT() (string, error) {
	now := time.Now()

	header := jwtHeader{
		Alg: "HS256",
		Typ: "JWT",
	}

	claims := jwtClaims{
		Iat: now.Unix(),
		Exp: now.Add(jwtExpiry).Unix(),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	signature := a.sign(signingInput)

	return signingInput + "." + signature, nil
}

// ValidateJWT validates a JWT and returns the claims if valid.
// Returns an error if the JWT is invalid or expired.
func (a *Auth) ValidateJWT(tokenString string) (*jwtClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	headerB64, claimsB64, signatureB64 := parts[0], parts[1], parts[2]

	// Verify signature
	signingInput := headerB64 + "." + claimsB64
	expectedSig := a.sign(signingInput)
	if signatureB64 != expectedSig {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode and verify header
	headerJSON, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}

	if header.Alg != "HS256" || header.Typ != "JWT" {
		return nil, fmt.Errorf("unsupported JWT type")
	}

	// Decode claims
	claimsJSON, err := base64.RawURLEncoding.DecodeString(claimsB64)
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims jwtClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

// sign creates an HMAC-SHA256 signature using the auth token as the secret.
func (a *Auth) sign(data string) string {
	h := hmac.New(sha256.New, []byte(a.token))
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
