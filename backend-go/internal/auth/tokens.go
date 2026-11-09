package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/identity"
)

const (
	RefreshTokenBytes = 64
	OneTimeTokenBytes = 48
)

var ErrInvalidAccessToken = identity.ErrInvalidToken

type Principal = identity.Principal

type TokenService struct {
	accessSecret  []byte
	accessTTL     time.Duration
	refreshTTLDay int
	now           func() time.Time
}

func NewTokenService(cfg config.JWTCfg) *TokenService {
	return &TokenService{
		accessSecret:  []byte(cfg.AccessSecret),
		accessTTL:     time.Duration(cfg.AccessTTLSec) * time.Second,
		refreshTTLDay: cfg.RefreshTTLDay,
		now:           time.Now,
	}
}

func (s *TokenService) SetClock(now func() time.Time) {
	s.now = now
}

func (s *TokenService) SignAccessToken(userID, role string) (string, error) {
	now := s.now()
	claims := jwt.MapClaims{
		"sub":  userID,
		"role": role,
		"jti":  uuid.NewString(),
		"iat":  now.Unix(),
		"exp":  now.Add(s.accessTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.accessSecret)
}

func (s *TokenService) VerifyAccessToken(raw string) (Principal, error) {
	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		return s.accessSecret, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil || !parsed.Valid {
		return Principal{}, ErrInvalidAccessToken
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Principal{}, ErrInvalidAccessToken
	}
	sub, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	jti, _ := claims["jti"].(string)
	if sub == "" || jti == "" {
		return Principal{}, ErrInvalidAccessToken
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return Principal{}, ErrInvalidAccessToken
	}
	if !s.now().Before(exp.Time) {
		return Principal{}, ErrInvalidAccessToken
	}
	return Principal{Subject: sub, Role: role, JTI: jti}, nil
}

type OpaqueToken struct {
	Raw       string
	Digest    string
	ExpiresAt time.Time
}

func (s *TokenService) NewRefreshToken() (OpaqueToken, error) {
	raw, err := randomOpaqueToken(RefreshTokenBytes)
	if err != nil {
		return OpaqueToken{}, err
	}
	return OpaqueToken{
		Raw:       raw,
		Digest:    Digest(raw),
		ExpiresAt: s.now().Add(time.Duration(s.refreshTTLDay) * 24 * time.Hour),
	}, nil
}

func (s *TokenService) NewOneTimeToken() (OpaqueToken, error) {
	raw, err := randomOpaqueToken(OneTimeTokenBytes)
	if err != nil {
		return OpaqueToken{}, err
	}
	return OpaqueToken{Raw: raw, Digest: Digest(raw)}, nil
}

func Digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomOpaqueToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func MarshalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
