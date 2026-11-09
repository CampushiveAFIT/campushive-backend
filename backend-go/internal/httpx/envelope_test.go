package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, rec.Body.String())
	}
	return payload
}

func TestWriteJSONSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]any{"id": "abc"})

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	payload := decodeBody(t, rec)
	if payload["success"] != true {
		t.Errorf("success = %v, want true", payload["success"])
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["id"] != "abc" {
		t.Errorf("unexpected data: %v", payload["data"])
	}
	if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), `{"success":true,"data":`) {
		t.Errorf("field order changed: %s", rec.Body.String())
	}
}

func TestWriteJSONNilData(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, nil)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"success":true,"data":null}` {
		t.Errorf("body = %s", got)
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
		wantDetail bool
	}{
		{"validation details", ValidationError([]string{"email must be an email address"}), 400, "VALIDATION_ERROR", "Validation failed.", true},
		{"validation message", ValidationMessage("Invalid pagination cursor."), 400, "VALIDATION_ERROR", "Invalid pagination cursor.", false},
		{"unauthorized", Unauthorized(CodeInvalidCredentials, "Authentication required."), 401, "INVALID_CREDENTIALS", "Authentication required.", false},
		{"expired token", Unauthorized(CodeExpiredToken, "Invalid or expired access token."), 401, "EXPIRED_TOKEN", "Invalid or expired access token.", false},
		{"forbidden", Forbidden("You cannot perform this action."), 403, "FORBIDDEN", "You cannot perform this action.", false},
		{"not found", NotFound(), 404, "NOT_FOUND", "Resource not found.", false},
		{"conflict", Conflict(CodeEventFull, "Event is full."), 409, "EVENT_FULL", "Event is full.", false},
		{"rate limited", TooManyRequests("Too many requests. Please try again later."), 429, "RATE_LIMITED", "Too many requests. Please try again later.", false},
		{"payload too large", PayloadTooLarge(), 413, "PAYLOAD_TOO_LARGE", "Request body too large.", false},
		{"internal", Internal(errTest{}), 500, "INTERNAL_ERROR", "An unexpected error occurred.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, tc.err)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			payload := decodeBody(t, rec)
			if payload["success"] != false {
				t.Errorf("success = %v, want false", payload["success"])
			}
			errorBody, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("missing error object: %s", rec.Body.String())
			}
			if errorBody["code"] != tc.wantCode {
				t.Errorf("code = %v, want %s", errorBody["code"], tc.wantCode)
			}
			if errorBody["message"] != tc.wantMsg {
				t.Errorf("message = %v, want %s", errorBody["message"], tc.wantMsg)
			}
			_, hasDetails := errorBody["details"]
			if hasDetails != tc.wantDetail {
				t.Errorf("details present = %t, want %t", hasDetails, tc.wantDetail)
			}
			if strings.Contains(rec.Body.String(), "errTest") {
				t.Errorf("internal error details leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestInternalErrorDoesNotLeakCause(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, Internal(errSecret{}))
	body := rec.Body.String()
	if strings.Contains(body, "super-secret") || strings.Contains(body, "sqlstate") {
		t.Fatalf("error cause leaked: %s", body)
	}
}

func TestWrappedAPIErrorIsClassified(t *testing.T) {
	wrapped := wrap(NotFound())
	status, code, _, _ := StatusForError(wrapped)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Errorf("status/code = %d/%s", status, code)
	}
}

type errTest struct{}

func (errTest) Error() string { return "boom" }

type errSecret struct{}

func (errSecret) Error() string { return "connect failed: password=hunter2" }
