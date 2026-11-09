package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

func testTokens() *TokenService {
	return NewTokenService(config.JWTCfg{
		AccessSecret:  "unit-test-access-secret-0123456789-abcdef",
		RefreshSecret: "unit-test-refresh-secret-0123456789-abcdef",
		AccessTTLSec:  900,
		RefreshTTLDay: 30,
	})
}

func TestAccessTokenCarriesExpectedClaims(t *testing.T) {
	tokens := testTokens()
	raw, err := tokens.SignAccessToken("user-1", "STUDENT")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("token must have three segments, got %d", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if !strings.Contains(string(header), `"alg":"HS256"`) {
		t.Errorf("header = %s, want HS256", header)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	for _, key := range []string{"sub", "role", "jti", "iat", "exp"} {
		if _, ok := claims[key]; !ok {
			t.Errorf("claim %q is missing: %v", key, claims)
		}
	}
	if claims["sub"] != "user-1" || claims["role"] != "STUDENT" {
		t.Errorf("claims = %v", claims)
	}
	if claims["jti"] == "" || claims["jti"] == nil {
		t.Error("jti must not be empty")
	}
	if claims["exp"].(float64)-claims["iat"].(float64) != 900 {
		t.Errorf("exp-iat = %v, want 900", claims["exp"].(float64)-claims["iat"].(float64))
	}

	principal, err := tokens.VerifyAccessToken(raw)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if principal.Subject != "user-1" || principal.Role != "STUDENT" {
		t.Errorf("principal = %+v", principal)
	}
	if principal.JTI == "" {
		t.Error("principal JTI is empty")
	}
}

func TestAccessTokensAreUniquePerIssue(t *testing.T) {
	tokens := testTokens()
	first, err := tokens.SignAccessToken("user-1", "STUDENT")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	second, err := tokens.SignAccessToken("user-1", "STUDENT")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	if first == second {
		t.Error("two issues of the same user must differ (jti)")
	}
}

func TestVerifyAccessTokenRejectsInvalidInputs(t *testing.T) {
	tokens := testTokens()
	valid, err := tokens.SignAccessToken("user-1", "STUDENT")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	other := NewTokenService(config.JWTCfg{AccessSecret: "a-different-secret-that-is-long-enough", AccessTTLSec: 900})

	noneAlg := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "user-1", "jti": "x", "exp": time.Now().Add(time.Hour).Unix()})
	unsigned, err := noneAlg.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "jti": "x", "exp": time.Now().Add(-time.Minute).Unix(),
	}).SignedString([]byte("unit-test-access-secret-0123456789-abcdef"))
	if err != nil {
		t.Fatalf("build expired token: %v", err)
	}
	missingJTI, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("unit-test-access-secret-0123456789-abcdef"))
	if err != nil {
		t.Fatalf("build token without jti: %v", err)
	}
	noExpiry, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "jti": "x",
	}).SignedString([]byte("unit-test-access-secret-0123456789-abcdef"))
	if err != nil {
		t.Fatalf("build token without exp: %v", err)
	}

	cases := map[string]string{
		"empty":               "",
		"garbage":             "not-a-token",
		"two segments":        strings.Join(strings.Split(valid, ".")[:2], "."),
		"tampered payload":    strings.Join(append(strings.Split(valid, ".")[:1], "eyJzdWIiOiJhdHRhY2tlciJ9", strings.Split(valid, ".")[2]), "."),
		"wrong secret":        mustSign(t, "another-secret-that-is-long-enough-x", "user-1"),
		"unsigned (alg=none)": unsigned,
		"expired":             expired,
		"missing jti":         missingJTI,
		"missing exp":         noExpiry,
	}
	for name, candidate := range cases {
		if _, err := tokens.VerifyAccessToken(candidate); err == nil {
			t.Errorf("%s: verification must fail", name)
		}
	}
	if _, err := other.VerifyAccessToken(valid); err == nil {
		t.Error("a token signed with another secret must not verify")
	}
}

func mustSign(t *testing.T, secret, subject string) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": subject, "jti": "x", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

func TestNewRefreshTokenShapeAndExpiry(t *testing.T) {
	tokens := testTokens()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tokens.SetClock(func() time.Time { return now })

	token, err := tokens.NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token.Raw)
	if err != nil {
		t.Fatalf("the refresh token must be base64url: %v", err)
	}
	if len(raw) != RefreshTokenBytes {
		t.Errorf("entropy = %d bytes, want %d", len(raw), RefreshTokenBytes)
	}
	if strings.ContainsAny(token.Raw, "+/=") {
		t.Errorf("token %q must be url-safe and unpadded", token.Raw)
	}
	if token.ExpiresAt != now.Add(30*24*time.Hour) {
		t.Errorf("expiresAt = %s, want %s", token.ExpiresAt, now.Add(30*24*time.Hour))
	}
	if token.Digest != Digest(token.Raw) {
		t.Error("digest must be the SHA-256 hex of the raw token")
	}
	if strings.Contains(token.Digest, token.Raw) {
		t.Error("the digest must not contain the raw token")
	}
}

func TestNewOneTimeTokenShape(t *testing.T) {
	tokens := testTokens()
	token, err := tokens.NewOneTimeToken()
	if err != nil {
		t.Fatalf("NewOneTimeToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token.Raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != OneTimeTokenBytes {
		t.Errorf("entropy = %d bytes, want %d", len(raw), OneTimeTokenBytes)
	}
	if !token.ExpiresAt.IsZero() {
		t.Errorf("one-time tokens carry no expiry, got %s", token.ExpiresAt)
	}
}

func TestTokensAreUnpredictable(t *testing.T) {
	tokens := testTokens()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		token, err := tokens.NewRefreshToken()
		if err != nil {
			t.Fatalf("NewRefreshToken: %v", err)
		}
		if seen[token.Raw] {
			t.Fatal("duplicate refresh token generated")
		}
		seen[token.Raw] = true
	}
}

func TestDigestMatchesSHA256Hex(t *testing.T) {
	inputs := []string{"", "abc", strings.Repeat("x", 1000)}
	for _, input := range inputs {
		sum := sha256.Sum256([]byte(input))
		if got, want := Digest(input), hex.EncodeToString(sum[:]); got != want {
			t.Errorf("Digest(%q) = %s, want %s", input, got, want)
		}
	}
}
