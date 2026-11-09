package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"campus-hive/backend-go/internal/validate"
)

type rawBodyKey struct{}

func PreParseBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			WriteError(w, decodeReadError(err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if len(raw) > 0 && bodyKind(r.Header.Get("Content-Type")) == bodyJSON {
			if scanErr := scanJSON(string(raw)); scanErr != nil {
				WriteError(w, ValidationMessage(scanErr.message))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, raw)))
	})
}

func rawBody(r *http.Request) ([]byte, bool) {
	raw, ok := r.Context().Value(rawBodyKey{}).([]byte)
	return raw, ok
}

// DecodeBody reads a request body the way the legacy express parsers do and
// keeps the raw shape of every property, so DTO validation can reproduce
// class-validator's behaviour for absent, null and non-string values. A body
// that is not a JSON object behaves exactly like an empty one, which is what
// class-transformer feeds the validator.
func DecodeBody(r *http.Request, known ...string) (*validate.Body, error) {
	raw, cached := rawBody(r)
	if !cached {
		body, err := r.Body, error(nil)
		if body == nil {
			body = http.NoBody
		}
		raw, err = io.ReadAll(body)
		if err != nil {
			return nil, decodeReadError(err)
		}
	}
	if len(raw) == 0 {
		return validate.NewBody(validate.Object{}, known...), nil
	}

	switch bodyKind(r.Header.Get("Content-Type")) {
	case bodyJSON:
		object, err := decodeJSONObject(raw)
		if err != nil {
			return nil, err
		}
		return validate.NewBody(object, known...), nil
	case bodyForm:
		object, err := decodeFormObject(raw)
		if err != nil {
			return nil, err
		}
		return validate.NewBody(object, known...), nil
	default:
		return validate.NewBody(validate.Object{}, known...), nil
	}
}

type bodyType int

const (
	bodyOther bodyType = iota
	bodyJSON
	bodyForm
)

func bodyKind(contentType string) bodyType {
	if contentType == "" {
		return bodyForm
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return bodyOther
	}
	switch mediaType {
	case "application/json":
		return bodyJSON
	case "application/x-www-form-urlencoded":
		return bodyForm
	default:
		return bodyOther
	}
}

func decodeJSONObject(raw []byte) (validate.Object, error) {
	trimmed := bytes.TrimSpace(raw)
	if scanErr := scanJSON(string(raw)); scanErr != nil {
		return validate.Object{}, ValidationMessage(scanErr.message)
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return validate.Object{}, nil
	}

	object := validate.Object{Values: map[string]json.RawMessage{}}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := decoder.Token(); err != nil {
		return validate.Object{}, ValidationMessage("Request body is not valid JSON.")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return validate.Object{}, ValidationMessage("Request body is not valid JSON.")
		}
		name, ok := key.(string)
		if !ok {
			return validate.Object{}, ValidationMessage("Request body is not valid JSON.")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return validate.Object{}, ValidationMessage("Request body is not valid JSON.")
		}
		if _, seen := object.Values[name]; !seen {
			object.Keys = append(object.Keys, name)
		}
		object.Values[name] = value
	}
	return object, nil
}

// decodeFormObject reproduces what qs gives the legacy validation pipe: keys in
// payload order, repeated keys collapsed into one, and bracket syntax
// reported under its base property.
func decodeFormObject(raw []byte) (validate.Object, error) {
	object := validate.Object{Values: map[string]json.RawMessage{}}
	for _, pair := range strings.Split(string(raw), "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		name := formKey(rawKey)
		if name == "" {
			continue
		}
		if _, seen := object.Values[name]; seen {
			continue
		}
		encoded, err := json.Marshal(decodeFormValue(rawValue))
		if err != nil {
			continue
		}
		object.Keys = append(object.Keys, name)
		object.Values[name] = encoded
	}
	return object, nil
}

func formKey(rawKey string) string {
	name, _, _ := strings.Cut(rawKey, "[")
	return decodeFormValue(name)
}

func decodeFormValue(raw string) string {
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func decodeReadError(err error) error {
	if isBodyTooLarge(err) {
		return PayloadTooLarge()
	}
	if errors.Is(err, io.EOF) {
		return ValidationMessage("Request body is required.")
	}
	return ValidationMessage("Request body is not valid JSON.")
}

func isBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "http: request body too large")
}

// RequireUUIDPath reads a route parameter and rejects anything that is not a
// UUID. NestJS ParseUUIDPipe accepts every version and reports a bare message
// with no details array, unlike the class-validator @IsUUID rules used for
// body properties.
func RequireUUIDPath(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	value := chi.URLParam(r, param)
	if !validate.IsUUID(value, 0) {
		WriteError(w, ValidationMessage("Validation failed (uuid is expected)"))
		return "", false
	}
	return value, true
}
