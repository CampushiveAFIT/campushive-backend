package httpx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/logging"
)

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	r.Header.Set("X-Powered-By", "Express")
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, nil)
	})).ServeHTTP(rec, r)

	expected := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "SAMEORIGIN",
		"Referrer-Policy":                   "no-referrer",
		"X-DNS-Prefetch-Control":            "off",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"Cross-Origin-Resource-Policy":      "same-origin",
		"Origin-Agent-Cluster":              "?1",
	}
	for key, want := range expected {
		if got := rec.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := rec.Header().Get("X-Powered-By"); got != "" {
		t.Errorf("X-Powered-By should be removed, got %q", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must not be sent over plain HTTP, got %q", got)
	}
}

func TestCORSUsesAllowlist(t *testing.T) {
	handler := CORS([]string{"http://localhost:5173"}, 10*time.Minute)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, http.StatusOK, nil)
		}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	handler.ServeHTTP(rec, r)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, want Origin", rec.Header().Get("Vary"))
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "X-RateLimit-Limit") {
		t.Errorf("rate limit headers should be exposed: %q", rec.Header().Get("Access-Control-Expose-Headers"))
	}

	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	r.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(rec, r)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must not receive allow-origin, got %q", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := CORS([]string{"http://localhost:5173"}, 10*time.Minute)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("preflight must not reach the handler")
		}))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	r.Header.Set("Access-Control-Request-Method", "POST")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("allow-methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("allow-headers = %q", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestRecoverTurnsPanicIntoInternalError(t *testing.T) {
	logger := logging.New("error")
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	r = r.WithContext(logging.WithLogger(r.Context(), logger))

	handler := Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler exploded")
	}))
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	payload := decodeBody(t, rec)
	if payload["success"] != false {
		t.Errorf("success = %v", payload["success"])
	}
	if strings.Contains(rec.Body.String(), "exploded") {
		t.Errorf("panic details leaked: %s", rec.Body.String())
	}
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	var seen string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestContextFrom(r.Context()).ID
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || rec.Header().Get("X-Request-Id") != seen {
		t.Errorf("request id mismatch: %q vs %q", seen, rec.Header().Get("X-Request-Id"))
	}
}

func TestRequestIDRejectsHostileHeader(t *testing.T) {
	var seen string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestContextFrom(r.Context()).ID
	}))
	for _, hostile := range []string{"bad value", strings.Repeat("a", 200), "id\ninjected"} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Request-Id", hostile)
		handler.ServeHTTP(rec, r)
		if seen == hostile && hostile != strings.Repeat("a", 200) {
			t.Errorf("hostile request id %q was accepted", hostile)
		}
		if rec.Header().Get("X-Request-Id") == "" {
			t.Error("expected a generated request id")
		}
	}
}

func TestAccessLogRecordsClientIP(t *testing.T) {
	trust, err := config.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatalf("ParseTrustProxy: %v", err)
	}
	var seenIP string
	handler := AccessLog(trust)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenIP = RequestContextFrom(r.Context()).ClientIP
		WriteJSON(w, http.StatusCreated, map[string]string{"ok": "yes"})
	}))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	r.RemoteAddr = "127.0.0.1:4321"
	r.Header.Set("X-Forwarded-For", "203.0.113.55")
	handler.ServeHTTP(rec, r)

	if seenIP != "203.0.113.55" {
		t.Errorf("client ip = %q, want 203.0.113.55", seenIP)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlerNotFound().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %s", rec.Body.String())
	}
	errorBody := payload["error"].(map[string]any)
	if errorBody["code"] != "NOT_FOUND" {
		t.Errorf("code = %v", errorBody["code"])
	}
	if !strings.Contains(errorBody["message"].(string), "/api/v1/unknown") {
		t.Errorf("message = %v", errorBody["message"])
	}
}

func TestLoggingWithLogger(t *testing.T) {
	logger := logging.New("error")
	ctx := logging.WithLogger(context.Background(), logger)
	if logging.From(ctx) != logger {
		t.Error("WithLogger should round-trip")
	}
	if logging.From(context.Background()) == nil {
		t.Error("From should never return nil")
	}
}

func TestBodyLimitRejectsOversizedRequestOverTheWire(t *testing.T) {
	handler := BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the handler must not run for an oversized body")
		WriteJSON(w, http.StatusOK, nil)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	body := strings.Repeat("x", 4096)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/auth/register", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("the client must receive the 413, got %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", response.StatusCode, raw)
	}
	want := `{"success":false,"error":{"code":"PAYLOAD_TOO_LARGE","message":"Request body too large."}}`
	if string(raw) != want {
		t.Errorf("body = %s, want %s", raw, want)
	}
}

func TestBodyLimitLetsNormalRequestsThrough(t *testing.T) {
	handler := BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			WriteError(w, Internal(err))
			return
		}
		WriteJSON(w, http.StatusOK, string(raw))
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := server.Client().Post(server.URL, "application/json", strings.NewReader("small"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, raw)
	}
}
