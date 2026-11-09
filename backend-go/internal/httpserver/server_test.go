package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/openapi"
	"campus-hive/backend-go/internal/testutil"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
)

type echoPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func defaultConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testutil.TestConfig(t)
	cfg.Port = 0
	cfg.NodeEnv = "test"
	return cfg
}

func newTestServer(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()
	srv := httpserver.New(httpserver.Options{
		Config: cfg,
		Logger: testutil.TestLogger(t),
		RegisterRoutes: func(api chi.Router, deps httpserver.Dependencies, _ *openapi.Document) {
			api.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
				httpx.WriteJSON(w, http.StatusOK, map[string]string{"pong": "ok"})
			})
			api.Post("/echo", func(w http.ResponseWriter, r *http.Request) {
				body, err := httpx.DecodeBody(r, "name", "count")
				if err != nil {
					httpx.WriteError(w, err)
					return
				}
				var failures validate.Errors
				failures.Whitelist(body.Unknown())
				if len(failures) > 0 {
					httpx.WriteError(w, httpx.ValidationError(failures))
					return
				}
				payload := echoPayload{}
				if name := body.String("name"); name.IsString {
					payload.Name = name.Value()
				}
				if count := body.Raw("count"); len(count) > 0 {
					if err := json.Unmarshal(count, &payload.Count); err != nil {
						httpx.WriteError(w, httpx.ValidationError(validate.Errors{"count must be a number"}))
						return
					}
				}
				httpx.WriteJSON(w, http.StatusOK, payload)
			})
		},
	})
	testServer := httptest.NewServer(srv.Router())
	t.Cleanup(testServer.Close)
	return testServer
}

func decodeError(t *testing.T, response *http.Response) struct {
	Success bool `json:"success"`
	Error   struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
} {
	t.Helper()
	var envelope struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string   `json:"code"`
			Message string   `json:"message"`
			Details []string `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Success {
		t.Error("error envelopes must set success=false")
	}
	return envelope
}

func TestHealthzReportsNotConfiguredInTestEnvironment(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	response, err := http.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
	var payload struct {
		Status   string `json:"status"`
		Database string `json:"database"`
		Redis    string `json:"redis"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "ok" || payload.Database != "not-configured" || payload.Redis != "not-configured" {
		t.Errorf("payload = %+v", payload)
	}
}

func TestHealthzReportsDownDependenciesOutsideTests(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.NodeEnv = "production"
	testServer := newTestServer(t, cfg)
	response, err := http.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", response.StatusCode)
	}
	var payload struct {
		Status   string `json:"status"`
		Database string `json:"database"`
		Redis    string `json:"redis"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "degraded" || payload.Database != "down" || payload.Redis != "down" {
		t.Errorf("payload = %+v", payload)
	}
}

func TestHealthzWithLiveDependencies(t *testing.T) {
	testutil.SkipIfNoDatabase(t)
	cfg := defaultConfig(t)
	cfg.NodeEnv = "production"
	cfg.DatabaseURL = testutil.DatabaseURL(t)
	srv := httpserver.New(httpserver.Options{
		Config: cfg,
		Logger: testutil.TestLogger(t),
		DB:     testutil.OpenDB(t),
		Redis:  testutil.StartRedis(t),
	})
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()

	response, err := http.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

func TestUnknownRouteReturnsNestStyleNotFoundEnvelope(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	response, err := http.Get(testServer.URL + "/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "NOT_FOUND" {
		t.Errorf("code = %q", envelope.Error.Code)
	}
	if envelope.Error.Message != "Cannot GET /does-not-exist" {
		t.Errorf("message = %q", envelope.Error.Message)
	}
}

func TestUnknownApiRouteKeepsApiPrefixInMessage(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	response, err := http.Post(testServer.URL+"/api/v1/nope", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Message != "Cannot POST /api/v1/nope" {
		t.Errorf("message = %q", envelope.Error.Message)
	}
}

func TestWrongMethodReturnsNestStyleNotFound(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	response, err := http.Post(testServer.URL+"/api/v1/ping", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Message != "Cannot POST /api/v1/ping" {
		t.Errorf("message = %q", envelope.Error.Message)
	}
}

func TestSuccessEnvelopeAndSecurityHeaders(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Origin", "http://localhost:5173")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("content-type = %q", got)
	}
	if response.Header.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id is missing")
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("cors origin = %q", got)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options is missing")
	}
	var envelope struct {
		Success bool              `json:"success"`
		Data    map[string]string `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !envelope.Success || envelope.Data["pong"] != "ok" {
		t.Errorf("envelope = %+v", envelope)
	}
}

func TestPreflightIsAnsweredWithoutAuth(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	request, err := http.NewRequest(http.MethodOptions, testServer.URL+"/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "POST")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 204 or 200", response.StatusCode)
	}
	if response.Header.Get("Access-Control-Allow-Methods") == "" {
		t.Error("Access-Control-Allow-Methods is missing")
	}
	if got := response.Header.Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("max-age = %q, want 600", got)
	}
}

func TestOversizedBodyIsRejectedWithPayloadTooLarge(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.MaxBodyBytes = 64
	testServer := newTestServer(t, cfg)
	response, err := http.Post(testServer.URL+"/api/v1/echo", "application/json",
		strings.NewReader(`{"data":"`+strings.Repeat("x", 512)+`"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.StatusCode)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "PAYLOAD_TOO_LARGE" {
		t.Errorf("code = %q", envelope.Error.Code)
	}
}

func TestStrictJSONRejectsUnknownFields(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))
	response, err := http.Post(testServer.URL+"/api/v1/echo", "application/json",
		strings.NewReader(`{"name":"go","surprise":true,"extra":"also unexpected"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Message != "Validation failed." {
		t.Errorf("message = %q", envelope.Error.Message)
	}
	if len(envelope.Error.Details) != 2 {
		t.Errorf("details = %v, want every unknown property reported", envelope.Error.Details)
	}
}

func TestOpenAPIDocumentAndSwaggerPage(t *testing.T) {
	testServer := newTestServer(t, defaultConfig(t))

	response, err := http.Get(testServer.URL + "/api-json")
	if err != nil {
		t.Fatalf("GET /api-json: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var document map[string]any
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if document["openapi"] == nil || document["paths"] == nil {
		t.Errorf("document is missing openapi/paths keys: %v", document)
	}

	pageResponse, err := http.Get(testServer.URL + "/api/docs")
	if err != nil {
		t.Fatalf("GET /api/docs: %v", err)
	}
	defer func() { _ = pageResponse.Body.Close() }()
	if pageResponse.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", pageResponse.StatusCode)
	}
	if got := pageResponse.Header.Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Errorf("content-type = %q", got)
	}
	body, err := io.ReadAll(pageResponse.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "swagger") {
		t.Error("the docs page does not reference swagger")
	}
	if unescaped := strings.ReplaceAll(string(body), `\/`, "/"); !strings.Contains(unescaped, "/api/docs-json") {
		t.Errorf("the docs page must load the spec from the legacy path: %s", body)
	}

	for _, path := range []string{"/api-json", "/api/docs-json"} {
		alias, err := http.Get(testServer.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		var aliasDocument map[string]any
		decodeErr := json.NewDecoder(alias.Body).Decode(&aliasDocument)
		_ = alias.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", path, decodeErr)
		}
		if aliasDocument["openapi"] == nil {
			t.Errorf("%s is not an OpenAPI document", path)
		}
	}
}

func TestDependenciesArePropagatedToRouteRegistration(t *testing.T) {
	testutil.SkipIfNoDatabase(t)
	cfg := defaultConfig(t)
	cfg.DatabaseURL = testutil.DatabaseURL(t)
	db := testutil.OpenDB(t)
	redisClient := testutil.StartRedis(t)
	logger := testutil.TestLogger(t)

	httpserver.New(httpserver.Options{
		Config: cfg,
		Logger: logger,
		DB:     db,
		Redis:  redisClient,
		RegisterRoutes: func(api chi.Router, deps httpserver.Dependencies, _ *openapi.Document) {
			if deps.DB != db {
				t.Error("DB was not propagated")
			}
			if deps.Redis != redisClient {
				t.Error("Redis was not propagated")
			}
			if deps.Logger != logger {
				t.Error("Logger was not propagated")
			}
			if deps.Config.DatabaseURL != cfg.DatabaseURL || deps.Config.JWT != cfg.JWT {
				t.Error("Config was not propagated")
			}
		},
	})
}

func TestGracefulShutdownStopsServing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}

	testutil.SkipIfNoDatabase(t)
	cfg := defaultConfig(t)
	cfg.Port = port
	cfg.DatabaseURL = testutil.DatabaseURL(t)
	srv := httpserver.New(httpserver.Options{
		Config: cfg,
		Logger: testutil.TestLogger(t),
		DB:     testutil.OpenDB(t),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	address := fmt.Sprintf("127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	served := false
	var lastErr error
	for time.Now().Before(deadline) && !served {
		response, requestErr := client.Get("http://" + address + "/healthz")
		if requestErr == nil {
			_ = response.Body.Close()
			served = true
			break
		}
		lastErr = requestErr
		time.Sleep(25 * time.Millisecond)
	}
	if !served {
		select {
		case runErr := <-done:
			t.Fatalf("server never accepted requests: %v (Run returned %v)", lastErr, runErr)
		default:
			t.Fatalf("server never accepted requests: %v", lastErr)
		}
	}

	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("server did not shut down")
	}

	if _, err := client.Get("http://" + address + "/healthz"); err == nil {
		t.Error("the server is still serving after shutdown")
	}
}
