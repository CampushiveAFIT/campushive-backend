package users

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campus-hive/backend-go/internal/httpx"
)

func decodeProfile(t *testing.T, body string) (ProfileUpdate, []string, error) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	update, err := DecodeProfileUpdate(request)
	if err == nil {
		return update, nil, nil
	}
	status, _, _, details := httpx.StatusForError(err)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for %s", status, body)
	}
	list, ok := details.([]string)
	if !ok {
		t.Fatalf("details = %#v", details)
	}
	return ProfileUpdate{}, list, err
}

func TestDecodeProfileUpdateMatchesLegacyDetails(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "unknown property",
			body: `{"role":"STAFF"}`,
			want: []string{"property role should not exist"},
		},
		{
			name: "unknown properties in payload order",
			body: `{"zzz":1,"role":"STAFF"}`,
			want: []string{"property zzz should not exist", "property role should not exist"},
		},
		{
			name: "bio number",
			body: `{"bio":123}`,
			want: []string{"bio must be shorter than or equal to 500 characters", "bio must be a string"},
		},
		{
			name: "blank avatar url",
			body: `{"avatarUrl":""}`,
			want: []string{"avatarUrl must be a URL address"},
		},
		{
			name: "avatar url number",
			body: `{"avatarUrl":123}`,
			want: []string{
				"avatarUrl must be shorter than or equal to 2048 characters",
				"avatarUrl must be a URL address",
			},
		},
		{
			name: "avatar url without protocol",
			body: `{"avatarUrl":"cdn.example.com/a.png"}`,
			want: []string{"avatarUrl must be a URL address"},
		},
		{
			name: "display name too long",
			body: `{"displayName":"` + strings.Repeat("x", 121) + `"}`,
			want: []string{"displayName must be shorter than or equal to 120 characters"},
		},
		{
			name: "bio too long",
			body: `{"bio":"` + strings.Repeat("x", 501) + `"}`,
			want: []string{"bio must be shorter than or equal to 500 characters"},
		},
		{
			name: "avatar url too long",
			body: `{"avatarUrl":"https://e.test/` + strings.Repeat("x", 2100) + `"}`,
			want: []string{
				"avatarUrl must be shorter than or equal to 2048 characters",
				"avatarUrl must be a URL address",
			},
		},
		{
			name: "null values are skipped",
			body: `{"displayName":null,"bio":null,"avatarUrl":null}`,
			want: nil,
		},
		{
			name: "empty strings are validated",
			body: `{"displayName":"","bio":""}`,
			want: nil,
		},
		{
			name: "empty body",
			body: ``,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			update, details, err := decodeProfile(t, tc.body)
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v (details %v)", err, details)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a validation error, got %+v", update)
			}
			if len(details) != len(tc.want) {
				t.Fatalf("details = %v, want %v", details, tc.want)
			}
			for i := range tc.want {
				if details[i] != tc.want[i] {
					t.Errorf("details[%d] = %q, want %q", i, details[i], tc.want[i])
				}
			}
		})
	}
}

func TestDecodeProfileUpdateKeepsAbsentNullAndValueApart(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me",
		strings.NewReader(`{"displayName":"  Ada  ","bio":null,"avatarUrl":"https://cdn.example.com/a.png"}`))
	request.Header.Set("Content-Type", "application/json")
	update, err := DecodeProfileUpdate(request)
	if err != nil {
		t.Fatalf("DecodeProfileUpdate: %v", err)
	}
	if !update.DisplayName.Present || update.DisplayName.Null || update.DisplayName.Text != "Ada" {
		t.Errorf("displayName = %+v", update.DisplayName)
	}
	if !update.Bio.Present || !update.Bio.Null {
		t.Errorf("bio = %+v", update.Bio)
	}
	if !update.AvatarURL.Present || update.AvatarURL.Null || update.AvatarURL.Text != "https://cdn.example.com/a.png" {
		t.Errorf("avatarUrl = %+v", update.AvatarURL)
	}
	if update.empty() {
		t.Error("update should not be empty")
	}
	if update.Bio.pointer() != nil {
		t.Error("a null bio must clear the column")
	}
}

func TestDecodeProfileUpdateRejectsMalformedJSON(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me", strings.NewReader(`{"bio":`))
	request.Header.Set("Content-Type", "application/json")
	_, err := DecodeProfileUpdate(request)
	if err == nil {
		t.Fatal("expected an error")
	}
	status, code, message, details := httpx.StatusForError(err)
	if status != http.StatusBadRequest || code != httpx.CodeValidationError {
		t.Errorf("status/code = %d/%s", status, code)
	}
	if message != "Unexpected end of JSON input" {
		t.Errorf("message = %q", message)
	}
	if details != nil {
		t.Errorf("details = %v, want none", details)
	}
}
