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
	argonMemory      uint32 = 64 * 1024
	argonIterations  uint32 = 3
	argonParallelism uint8  = 4
	argonSaltLength  int    = 16
	argonKeyLength   uint32 = 32
	argonVersion     int    = 19
)

var errInvalidHash = errors.New("invalid argon2id hash")

type Hasher struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  int
	keyLength   uint32
}

func NewHasher() *Hasher {
	return &Hasher{
		memory:      argonMemory,
		iterations:  argonIterations,
		parallelism: argonParallelism,
		saltLength:  argonSaltLength,
		keyLength:   argonKeyLength,
	}
}

func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.iterations, h.memory, h.parallelism, h.keyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion,
		h.memory,
		h.iterations,
		h.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h *Hasher) Verify(encoded, password string) bool {
	params, salt, want, err := parseHash(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type hashParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseHash(encoded string) (hashParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return hashParams{}, nil, nil, errInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argonVersion {
		return hashParams{}, nil, nil, errInvalidHash
	}
	var params hashParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.iterations, &params.parallelism); err != nil {
		return hashParams{}, nil, nil, errInvalidHash
	}
	if params.memory == 0 || params.iterations == 0 || params.parallelism == 0 {
		return hashParams{}, nil, nil, errInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return hashParams{}, nil, nil, errInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return hashParams{}, nil, nil, errInvalidHash
	}
	if len(key) < 4 {
		return hashParams{}, nil, nil, errInvalidHash
	}
	return params, salt, key, nil
}
