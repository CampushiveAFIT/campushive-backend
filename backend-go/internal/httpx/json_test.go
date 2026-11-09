package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"campus-hive/backend-go/internal/validate"
)

func requestWithBody(body string, contentType string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestDecodeBodyParsesJSONObject(t *testing.T) {
	body, err := DecodeBody(requestWithBody(`{"email":"a@b.test","displayName":"Ada","extra":1}`, "application/json"),
		"email", "displayName")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	email := body.String("email")
	if !email.Present() || !email.IsString || email.Value() != "a@b.test" {
		t.Errorf("email = %+v", email)
	}
	if got := body.Unknown(); !slices.Equal(got, []string{"extra"}) {
		t.Errorf("unknown = %v", got)
	}
}

func TestDecodeBodyKeepsPayloadKeyOrder(t *testing.T) {
	body, err := DecodeBody(requestWithBody(`{"zzz":1,"email":"a@b.test","aaa":2,"mmm":3}`, "application/json"),
		"email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if got := body.Unknown(); !slices.Equal(got, []string{"zzz", "aaa", "mmm"}) {
		t.Errorf("unknown = %v, want payload order", got)
	}
}

func TestDecodeBodyReportsJSONSyntaxErrors(t *testing.T) {
	_, err := DecodeBody(requestWithBody(`{"email":`, "application/json"), "email")
	if err == nil {
		t.Fatal("expected an error")
	}
	status, code, message, details := StatusForError(err)
	if status != http.StatusBadRequest || code != CodeValidationError {
		t.Errorf("status/code = %d/%s", status, code)
	}
	if message != "Unexpected end of JSON input" {
		t.Errorf("message = %q", message)
	}
	if details != nil {
		t.Errorf("details = %v, want none", details)
	}
}

func TestDecodeBodyTreatsNonObjectJSONAsEmpty(t *testing.T) {
	for _, source := range []string{`[]`, `null`, `"text"`, `5`, `true`} {
		body, err := DecodeBody(requestWithBody(source, "application/json"), "email", "password")
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if body.String("email").Present() {
			t.Errorf("%s: expected no fields", source)
		}
		if len(body.Unknown()) != 0 {
			t.Errorf("%s: unknown = %v", source, body.Unknown())
		}
	}
}

func TestDecodeBodyRoutesByContentType(t *testing.T) {
	body, err := DecodeBody(requestWithBody(`{"email":"a@b.test","password":"x"}`, "text/plain"), "email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if body.String("email").Present() || len(body.Unknown()) != 0 {
		t.Errorf("text/plain must behave like an empty body, got %+v", body)
	}

	form := `{"email":"a@b.test","password":"x"}`
	body, err = DecodeBody(requestWithBody(form, "application/x-www-form-urlencoded"), "email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if got := body.Unknown(); !slices.Equal(got, []string{form}) {
		t.Errorf("form encoded unknown = %v", got)
	}

	// A request without a content type is parsed as a form, so a JSON payload
	// arrives as one property named after the whole body.
	body, err = DecodeBody(requestWithBody(form, ""), "email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if got := body.Unknown(); !slices.Equal(got, []string{form}) {
		t.Errorf("missing content type unknown = %v", got)
	}
	if body.String("email").Present() {
		t.Errorf("missing content type email = %+v, want absent", body.String("email"))
	}
}

func TestDecodeBodyFormKeyHandling(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{`zzz=1&aaa=2`, []string{"zzz", "aaa"}},
		{`aaa=2&zzz=1`, []string{"aaa", "zzz"}},
		{`zzz=1&zzz=2`, []string{"zzz"}},
		{`zzz[a]=1&aaa=2`, []string{"zzz", "aaa"}},
		{`zzz%5Ba%5D=1`, []string{"zzz[a]"}},
		{`empty&`, []string{"empty"}},
	}
	for _, tc := range cases {
		body, err := DecodeBody(requestWithBody(tc.body, "application/x-www-form-urlencoded"), "email")
		if err != nil {
			t.Fatalf("%q: %v", tc.body, err)
		}
		if got := body.Unknown(); !slices.Equal(got, tc.want) {
			t.Errorf("%q: unknown = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestDecodeBodyJSONKeysAreDeduplicated(t *testing.T) {
	body, err := DecodeBody(requestWithBody(`{"email":"first@example.com","zzz":1,"email":"second@example.com"}`, "application/json"),
		"email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if got := body.Unknown(); !slices.Equal(got, []string{"zzz"}) {
		t.Errorf("unknown = %v", got)
	}
	if got := body.String("email").Value(); got != "second@example.com" {
		t.Errorf("email = %q, want the last value like JSON.parse", got)
	}
}

func TestDecodeBodyRejectsOversizedPayload(t *testing.T) {
	source := `{"displayName":"` + strings.Repeat("a", 2*1024*1024) + `"}`
	r := requestWithBody(source, "application/json")
	r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, 1024)
	_, err := DecodeBody(r, "displayName")
	if err == nil {
		t.Fatal("expected an error")
	}
	if status, code, _, _ := StatusForError(err); status != http.StatusRequestEntityTooLarge || code != CodePayloadTooLarge {
		t.Errorf("status/code = %d/%s", status, code)
	}
}

func TestDecodeBodyTreatsEmptyBodyAsEmptyObject(t *testing.T) {
	body, err := DecodeBody(requestWithBody("", "application/json"), "email")
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if body.String("email").Present() || len(body.Unknown()) != 0 {
		t.Errorf("body = %+v", body)
	}
}

func TestFieldOptionalMirrorsIsOptional(t *testing.T) {
	object := validate.Object{
		Keys: []string{"null", "blank", "value", "number"},
		Values: map[string]json.RawMessage{
			"null":   json.RawMessage("null"),
			"blank":  json.RawMessage(`""`),
			"value":  json.RawMessage(`"x"`),
			"number": json.RawMessage("5"),
		},
	}
	body := validate.NewBody(object, "absent", "null", "blank", "value", "number")
	for _, name := range []string{"absent", "null"} {
		if !body.String(name).Optional().Skipped() {
			t.Errorf("%s: expected the field to be skipped", name)
		}
	}
	if body.String("blank").Optional().Skipped() {
		t.Error("an empty string is still validated by @IsOptional")
	}
	if body.String("value").Optional().Skipped() {
		t.Error("value must not be skipped")
	}
	number := body.String("number")
	if number.Skipped() || number.IsString || number.Value() != "" {
		t.Errorf("number = %+v", number)
	}
	if got := body.String("unknown").Value(); got != "" {
		t.Errorf("unknown field value = %q", got)
	}
}
