package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const tokenByteLength = 32

// GenerateSecureToken creates a cryptographically random token.
// Returns:
//   - rawToken: the raw bytes — send this in the cookie or reset link URL
//   - hash:     SHA-256 hex string — store ONLY this in the database, never the raw token
//
// crypto/rand is used exclusively — never math/rand which is not cryptographically secure.
func GenerateSecureToken() (rawToken []byte, hash string, err error) {
	rawToken = make([]byte, tokenByteLength)

	if _, err = rand.Read(rawToken); err != nil {
		return nil, "", fmt.Errorf("crypto: failed to generate secure token: %w", err)
	}

	hash = HashToken(rawToken)
	return rawToken, hash, nil
}

// HashToken returns the SHA-256 hex hash of a raw token.
// Use this when you receive a token from a cookie or URL and need to look it up in the DB.
// The DB stores hashes — never raw tokens.
func HashToken(rawToken []byte) string {
	sum := sha256.Sum256(rawToken)
	return hex.EncodeToString(sum[:])
}

// TokenToString encodes raw token bytes to a hex string safe for use in URLs and cookies.
func TokenToString(rawToken []byte) string {
	return hex.EncodeToString(rawToken)
}

// TokenFromString decodes a hex string back to raw token bytes.
// Use this when reading a token from a cookie or URL parameter before hashing for DB lookup.
func TokenFromString(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: invalid token string: %w", err)
	}
	return b, nil
}
