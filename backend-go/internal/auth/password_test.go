package auth

import (
	"regexp"
	"strings"
	"testing"
)

var phcPattern = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$`)

func TestHashProducesNodeArgon2CompatiblePHCString(t *testing.T) {
	hasher := NewHasher()
	encoded, err := hasher.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !phcPattern.MatchString(encoded) {
		t.Fatalf("hash %q does not match the argon2id PHC layout", encoded)
	}
	if strings.Contains(encoded, "Str0ng-Passw0rd!") {
		t.Fatal("the hash must not contain the plaintext password")
	}
}

func TestHashIsSaltedAndVerifiable(t *testing.T) {
	hasher := NewHasher()
	first, err := hasher.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := hasher.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second {
		t.Error("two hashes of the same password must differ (salted)")
	}
	if !hasher.Verify(first, "Str0ng-Passw0rd!") {
		t.Error("the correct password must verify")
	}
	if !hasher.Verify(second, "Str0ng-Passw0rd!") {
		t.Error("the correct password must verify against the second hash")
	}
}

func TestVerifyRejectsWrongPasswordsAndMalformedHashes(t *testing.T) {
	hasher := NewHasher()
	encoded, err := hasher.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	cases := map[string]string{
		"wrong password":       "Str0ng-Passw0rd",
		"empty password":       "",
		"bcrypt hash":          "$2b$12$abcdefghijklmnopqrstuv",
		"argon2i hash":         strings.Replace(encoded, "argon2id", "argon2i", 1),
		"missing salt":         "$argon2id$v=19$m=65536,t=3,p=4$$",
		"unsupported version":  strings.Replace(encoded, "v=19", "v=16", 1),
		"not a hash at all":    "plaintext",
		"empty string":         "",
		"truncated parameters": "$argon2id$v=19$m=0,t=3,p=4$aaaa$bbbb",
	}
	for name, candidate := range cases {
		if hasher.Verify(candidate, "Str0ng-Passw0rd!") {
			t.Errorf("%s: verify must return false", name)
		}
	}
}

func TestVerifyHandlesUnicodePasswords(t *testing.T) {
	hasher := NewHasher()
	const password = "Ünïcödé-Passw0rd!"
	encoded, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !hasher.Verify(encoded, password) {
		t.Error("unicode passwords must round-trip")
	}
	if hasher.Verify(encoded, "Ünïcödé-Passw0r") {
		t.Error("a near-miss unicode password must not verify")
	}
}
