package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024 // 64MB
	argonIterations  = 2
	argonParallelism = 4 // hardcoded — runtime.NumCPU() causes param mismatch across hardware
	argonKeyLength   = 32
	argonSaltLength  = 16
)

var (
	ErrInvalidHash         = errors.New("argon2: invalid hash format")
	ErrIncompatibleVersion = errors.New("argon2: incompatible version")
	ErrPasswordMismatch    = errors.New("argon2: password does not match hash")
)

// dummyHash is a syntactically valid Argon2id hash (all-zero salt and output).
// Its sole purpose is to let NormalizeTiming run the full key derivation so that
// "user not found" and "wrong password" login paths take the same wall time,
// preventing email enumeration via response-time differences.
const dummyHash = "$argon2id$v=19$m=65536,t=2,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// NormalizeTiming runs a full Argon2id derivation against a dummy hash.
// Call this whenever a login fails because the email does not exist, so the
// response time is indistinguishable from a failed password comparison.
func NormalizeTiming(password string) {
	ComparePassword(password, dummyHash) //nolint:errcheck — result intentionally ignored
}

// HashPassword hashes a plaintext password using Argon2id.
// The returned string contains the salt and all parameters — store only this, never the raw password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2: failed to generate salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		argonIterations,
		argonMemory,
		argonParallelism,
		argonKeyLength,
	)

	// Encode as a self-describing string so parameters are stored with the hash.
	// Format: $argon2id$v=<version>$m=<memory>,t=<iterations>,p=<parallelism>$<salt>$<hash>
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)

	return encoded, nil
}

// ComparePassword checks a plaintext password against a stored Argon2id hash.
// Uses subtle.ConstantTimeCompare to prevent timing attacks — never use == on hashes.
func ComparePassword(password, encodedHash string) error {
	// Parse the stored hash string to extract parameters and salt.
	parts := strings.Split(encodedHash, "$")
	// Expected: ["", "argon2id", "v=19", "m=65536,t=2,p=4", "<salt>", "<hash>"]
	if len(parts) != 6 {
		return ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return ErrInvalidHash
	}
	if version != argon2.Version {
		return ErrIncompatibleVersion
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidHash
	}

	storedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidHash
	}

	// Recompute hash with the same parameters and salt.
	computedHash := argon2.IDKey(
		[]byte(password),
		salt,
		iterations,
		memory,
		parallelism,
		uint32(len(storedHash)),
	)

	// Constant-time comparison — prevents timing-based attacks.
	if subtle.ConstantTimeCompare(storedHash, computedHash) != 1 {
		return ErrPasswordMismatch
	}

	return nil
}
