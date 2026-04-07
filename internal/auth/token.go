package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	accessTokenDuration = 15 * time.Minute
)

// Claims is the payload embedded in the access token JWT.
// sub = user ID, Email = user email, standard exp/iat/nbf from RegisteredClaims.
type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// TokenPair holds both tokens returned after a successful login or rotation.
type TokenPair struct {
	AccessToken  string // JWT — send as HttpOnly cookie
	RefreshToken []byte // raw random bytes — send as HttpOnly cookie, store SHA-256 hash in DB
}

// Keys holds the parsed Ed25519 key pair used for signing and verification.
type Keys struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// LoadKeys decodes base64-encoded raw Ed25519 keys from environment variable values.
// Keys must be raw bytes (private: 64 bytes, public: 32 bytes), base64-encoded.
// Generate with: go run ./cmd/keygen
// Call this at startup — pass the raw string values from os.Getenv.
func LoadKeys(privateB64, publicB64 string) (*Keys, error) {
	privateBytes, err := base64.StdEncoding.DecodeString(privateB64)
	if err != nil {
		return nil, fmt.Errorf("token: failed to decode private key: %w", err)
	}
	if len(privateBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("token: private key must be %d bytes (got %d) — regenerate with go run ./cmd/keygen", ed25519.PrivateKeySize, len(privateBytes))
	}

	publicBytes, err := base64.StdEncoding.DecodeString(publicB64)
	if err != nil {
		return nil, fmt.Errorf("token: failed to decode public key: %w", err)
	}
	if len(publicBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("token: public key must be %d bytes (got %d) — regenerate with go run ./cmd/keygen", ed25519.PublicKeySize, len(publicBytes))
	}

	return &Keys{
		Private: ed25519.PrivateKey(privateBytes),
		Public:  ed25519.PublicKey(publicBytes),
	}, nil
}

// NewAccessToken creates a signed JWT access token for the given user.
// Explicitly uses jwt.SigningMethodEdDSA — blocks alg:none and algorithm confusion attacks.
func (k *Keys) NewAccessToken(userID, email string) (string, error) {
	now := time.Now()

	claims := Claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,                                           // sub = user ID
			IssuedAt:  jwt.NewNumericDate(now),                          // iat
			NotBefore: jwt.NewNumericDate(now),                          // nbf
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenDuration)), // exp
		},
	}

	// jwt.SigningMethodEdDSA is explicit — never use jwt.Parse or allow alg switching.
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)

	signed, err := token.SignedString(k.Private)
	if err != nil {
		return "", fmt.Errorf("token: failed to sign access token: %w", err)
	}

	return signed, nil
}

// ValidateAccessToken parses and validates a JWT access token.
// Uses jwt.ParseWithClaims — never jwt.Parse.
// Explicitly verifies the signing method is EdDSA inside KeyFunc to block alg:none attacks.
func (k *Keys) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Critical: verify algorithm is EdDSA before returning the key.
		// If we skip this check an attacker can send alg:none and bypass verification.
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("token: unexpected signing method: %v", token.Header["alg"])
		}
		return k.Public, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, jwt.ErrTokenExpired // caller checks this to attempt refresh
		}
		return nil, fmt.Errorf("token: invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("token: invalid claims")
	}

	return claims, nil
}
