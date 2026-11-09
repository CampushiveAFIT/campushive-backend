package auth

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"campus-hive/backend-go/internal/httpx"
)

func decode(t *testing.T, path, body string, fn func(*http.Request) error) []string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	err := fn(r)
	if err == nil {
		return nil
	}
	_, code, _, details := httpx.StatusForError(err)
	if code != httpx.CodeValidationError {
		return nil
	}
	messages, ok := details.([]string)
	if !ok {
		return nil
	}
	return messages
}

func TestRegisterValidationMatchesClassValidator(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"empty object",
			`{}`,
			[]string{
				"email must be shorter than or equal to 255 characters",
				"email must be a valid email address",
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be shorter than or equal to 128 characters",
				"password must be at least 8 characters",
				"password must be a string",
			},
		},
		{
			"blank email",
			`{"email":"","password":"Abcdefg1"}`,
			[]string{"email must be a valid email address"},
		},
		{
			"null email",
			`{"email":null,"password":"Abcdefg1"}`,
			[]string{
				"email must be shorter than or equal to 255 characters",
				"email must be a valid email address",
			},
		},
		{
			"numeric email",
			`{"email":123,"password":"Abcdefg1"}`,
			[]string{
				"email must be shorter than or equal to 255 characters",
				"email must be a valid email address",
			},
		},
		{
			"numeric password",
			`{"email":"a@b.com","password":12345678}`,
			[]string{
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be shorter than or equal to 128 characters",
				"password must be at least 8 characters",
				"password must be a string",
			},
		},
		{
			"null password",
			`{"email":"a@b.com","password":null}`,
			[]string{
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be shorter than or equal to 128 characters",
				"password must be at least 8 characters",
				"password must be a string",
			},
		},
		{
			"short password",
			`{"email":"a@b.com","password":"Ab1!x"}`,
			[]string{
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be at least 8 characters",
			},
		},
		{
			"upper case only password",
			`{"email":"a@b.com","password":"ABCDEFG1"}`,
			[]string{"password must contain at least one lowercase letter, one uppercase letter, and one digit"},
		},
		{
			"empty password",
			`{"email":"a@b.com","password":""}`,
			[]string{
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be at least 8 characters",
			},
		},
		{
			"long password",
			`{"email":"a@b.com","password":"` + strings.Repeat("aA1", 50) + `"}`,
			[]string{"password must be shorter than or equal to 128 characters"},
		},
		{
			"long display name",
			`{"email":"a@b.com","password":"Abcdefg1","displayName":"` + strings.Repeat("x", 121) + `"}`,
			[]string{"displayName must be shorter than or equal to 120 characters"},
		},
		{
			"long email",
			`{"email":"` + strings.Repeat("a", 250) + `@b.com","password":"Abcdefg1"}`,
			[]string{
				"email must be shorter than or equal to 255 characters",
				"email must be a valid email address",
			},
		},
		{
			"unknown property",
			`{"email":"a@b.com","password":"Abcdefg1","role":"ADMIN"}`,
			[]string{"property role should not exist"},
		},
		{
			"unknown properties keep payload order",
			`{"zzz":1,"email":"nope","aaa":2,"password":"x"}`,
			[]string{
				"property zzz should not exist",
				"property aaa should not exist",
				"email must be a valid email address",
				"password must contain at least one lowercase letter, one uppercase letter, and one digit",
				"password must be at least 8 characters",
			},
		},
		{"array body", `[]`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
			"password must contain at least one lowercase letter, one uppercase letter, and one digit",
			"password must be shorter than or equal to 128 characters",
			"password must be at least 8 characters",
			"password must be a string",
		}},
		{"null body", `null`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
			"password must contain at least one lowercase letter, one uppercase letter, and one digit",
			"password must be shorter than or equal to 128 characters",
			"password must be at least 8 characters",
			"password must be a string",
		}},
		{"string body", `"hello"`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
			"password must contain at least one lowercase letter, one uppercase letter, and one digit",
			"password must be shorter than or equal to 128 characters",
			"password must be at least 8 characters",
			"password must be a string",
		}},
		{"number body", `5`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
			"password must contain at least one lowercase letter, one uppercase letter, and one digit",
			"password must be shorter than or equal to 128 characters",
			"password must be at least 8 characters",
			"password must be a string",
		}},
		{"boolean body", `true`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
			"password must contain at least one lowercase letter, one uppercase letter, and one digit",
			"password must be shorter than or equal to 128 characters",
			"password must be at least 8 characters",
			"password must be a string",
		}},
		{"object email", `{"email":{"a":1},"password":"Abcdefg1"}`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
		}},
		{"array email", `{"email":["a@b.com"],"password":"Abcdefg1"}`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be a valid email address",
		}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/register", tc.body, func(r *http.Request) error {
			_, err := DecodeRegister(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestRegisterAcceptsLegacyValidPayloads(t *testing.T) {
	valid := []string{
		`{"email":"a@b.com","password":"Abcdefg1","displayName":"Ada"}`,
		`{"email":"a@b.com","password":"Abcdefg1"}`,
		`{"email":"a@b.com","password":"Abcdefg1","displayName":"   "}`,
		`{"email":"a@b.com","password":"Abcdefg1","displayName":null}`,
		`{"email":"  ADA@Example.COM  ","password":"Str0ng-Passw0rd!","displayName":"  Ada  "}`,
	}
	for _, body := range valid {
		if got := decode(t, "/auth/register", body, func(r *http.Request) error {
			_, err := DecodeRegister(r)
			return err
		}); got != nil {
			t.Errorf("%s: unexpected errors %q", body, got)
		}
	}
}

func TestLoginValidationMatchesClassValidator(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{`{"email":"a@b.com","password":"x"}`, nil},
		{`{"email":"a@b.com"}`, []string{
			"password must be shorter than or equal to 128 characters",
			"password must be a string",
		}},
		{`{"email":"","password":"x"}`, []string{"email must be an email"}},
		{`{}`, []string{
			"email must be shorter than or equal to 255 characters",
			"email must be an email",
			"password must be shorter than or equal to 128 characters",
			"password must be a string",
		}},
		{`{"email":"a@b.com","password":1}`, []string{
			"password must be shorter than or equal to 128 characters",
			"password must be a string",
		}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/login", tc.body, func(r *http.Request) error {
			_, err := DecodeLogin(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.body, got, tc.want)
		}
	}
}

func TestRefreshValidationMatchesClassValidator(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{`{"refreshToken":"` + strings.Repeat("a", 32) + `"}`, nil},
		{`{"refreshToken":"` + strings.Repeat("a", 64) + `"}`, nil},
		{`{"refreshToken":"abc"}`, []string{"refreshToken must be longer than or equal to 32 characters"}},
		{`{"refreshToken":""}`, []string{"refreshToken must be longer than or equal to 32 characters"}},
		{`{"refreshToken":"` + strings.Repeat("a", 257) + `"}`, []string{"refreshToken must be shorter than or equal to 256 characters"}},
		{`{"token":"` + strings.Repeat("a", 32) + `"}`, []string{
			"property token should not exist",
			"refreshToken must be shorter than or equal to 256 characters",
			"refreshToken must be longer than or equal to 32 characters",
			"refreshToken must be a string",
		}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/refresh", tc.body, func(r *http.Request) error {
			_, err := DecodeRefreshToken(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.body, got, tc.want)
		}
	}
}

func TestPasswordChangeValidationMatchesClassValidator(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{`{"currentPassword":"Old-Passw0rd!","newPassword":"New-Passw0rd1"}`, nil},
		{`{"currentPassword":"","newPassword":"New-Passw0rd1"}`, nil},
		{`{"currentPassword":"Same-Passw0rd1","newPassword":"Same-Passw0rd1"}`, nil},
		{`{"currentPassword":"Old-Passw0rd!","newPassword":"weak"}`, []string{
			"newPassword must contain at least one lowercase letter, one uppercase letter, and one digit",
			"newPassword must be at least 8 characters",
		}},
		{`{"newPassword":"New-Passw0rd1"}`, []string{
			"currentPassword must be shorter than or equal to 128 characters",
			"currentPassword must be a string",
		}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/password/change", tc.body, func(r *http.Request) error {
			_, err := DecodePasswordChange(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.body, got, tc.want)
		}
	}
}

func TestPasswordResetValidationMatchesClassValidator(t *testing.T) {
	got := decode(t, "/auth/password/reset/request", `{"email":""}`, func(r *http.Request) error {
		_, err := DecodePasswordResetRequest(r)
		return err
	})
	if !slices.Equal(got, []string{"email must be an email"}) {
		t.Errorf("blank email: got %q", got)
	}
	got = decode(t, "/auth/password/reset/request", `{"email":"a@b.com","extra":1}`, func(r *http.Request) error {
		_, err := DecodePasswordResetRequest(r)
		return err
	})
	if !slices.Equal(got, []string{"property extra should not exist"}) {
		t.Errorf("unknown property: got %q", got)
	}
	if got := decode(t, "/auth/password/reset/request", `{"email":"a@b.com"}`, func(r *http.Request) error {
		_, err := DecodePasswordResetRequest(r)
		return err
	}); got != nil {
		t.Errorf("valid request rejected: %q", got)
	}

	longToken := strings.Repeat("a", 40)
	cases := []struct {
		body string
		want []string
	}{
		{`{"token":"` + longToken + `","newPassword":"New-Passw0rd1"}`, nil},
		{`{"token":"abc","newPassword":"New-Passw0rd1"}`, []string{"token must be longer than or equal to 32 characters"}},
		{`{"token":"` + longToken + `","newPassword":"weak"}`, []string{
			"newPassword must contain at least one lowercase letter, one uppercase letter, and one digit",
			"newPassword must be at least 8 characters",
		}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/password/reset/confirm", tc.body, func(r *http.Request) error {
			_, err := DecodePasswordResetConfirm(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.body, got, tc.want)
		}
	}
}

func TestEmailVerifyValidationMatchesClassValidator(t *testing.T) {
	if got := decode(t, "/auth/email/verify/request", `{"email":""}`, func(r *http.Request) error {
		_, err := DecodeEmailVerifyRequest(r)
		return err
	}); !slices.Equal(got, []string{"email must be an email"}) {
		t.Errorf("blank email: got %q", got)
	}
	cases := []struct {
		body string
		want []string
	}{
		{`{"token":"` + strings.Repeat("a", 40) + `"}`, nil},
		{`{"token":"abc"}`, []string{"token must be longer than or equal to 32 characters"}},
	}
	for _, tc := range cases {
		got := decode(t, "/auth/email/verify/confirm", tc.body, func(r *http.Request) error {
			_, err := DecodeEmailVerifyConfirm(r)
			return err
		})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.body, got, tc.want)
		}
	}
}

func TestRegisterTransformsMatchLegacy(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"email":"  ADA.Lovelace@Example.COM ","password":"Str0ng-Passw0rd!","displayName":"  Ada Lovelace  "}`))
	r.Header.Set("Content-Type", "application/json")
	request, err := DecodeRegister(r)
	if err != nil {
		t.Fatalf("DecodeRegister: %v", err)
	}
	if request.Email != "ada.lovelace@example.com" {
		t.Errorf("email = %q", request.Email)
	}
	if request.DisplayName == nil || *request.DisplayName != "Ada Lovelace" {
		t.Errorf("displayName = %v", request.DisplayName)
	}

	r = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"email":"a@b.com","password":"Abcdefg1"}`))
	r.Header.Set("Content-Type", "application/json")
	request, err = DecodeRegister(r)
	if err != nil {
		t.Fatalf("DecodeRegister: %v", err)
	}
	if request.DisplayName != nil {
		t.Errorf("displayName = %v, want nil when omitted", *request.DisplayName)
	}
}

func TestMalformedBodiesAreRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty", ``},
		{"truncated", `{"email":`},
		{"trailing garbage", `{"email":"a@b.com","password":"Abcdefg1"} oops`},
		{"two objects", `{"email":"a@b.com","password":"Abcdefg1"}{"email":"a@b.com"}`},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if _, err := DecodeRegister(r); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"a@b.com"}`))
	r.Header.Set("Content-Type", "text/plain")
	if _, err := DecodeRegister(r); err == nil {
		t.Error("non-JSON content type must be rejected")
	}
}
