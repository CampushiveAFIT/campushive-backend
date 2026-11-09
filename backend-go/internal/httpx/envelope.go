package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

type ErrorCode string

const (
	CodeValidationError    ErrorCode = "VALIDATION_ERROR"
	CodeInvalidCredentials ErrorCode = "INVALID_CREDENTIALS"
	CodeDuplicateEmail     ErrorCode = "DUPLICATE_EMAIL"
	CodeExpiredToken       ErrorCode = "EXPIRED_TOKEN"
	CodeRevokedToken       ErrorCode = "REVOKED_TOKEN"
	CodeWeakPassword       ErrorCode = "WEAK_PASSWORD"
	CodeNotFound           ErrorCode = "NOT_FOUND"
	CodeForbidden          ErrorCode = "FORBIDDEN"
	CodeRateLimited        ErrorCode = "RATE_LIMITED"
	CodeConflict           ErrorCode = "CONFLICT"
	CodePayloadTooLarge    ErrorCode = "PAYLOAD_TOO_LARGE"
	CodeEventFull          ErrorCode = "EVENT_FULL"
	CodeRegistrationClosed ErrorCode = "REGISTRATION_CLOSED"
	CodeInternalError      ErrorCode = "INTERNAL_ERROR"
)

type APIError struct {
	Status  int
	Code    ErrorCode
	Message string
	Details any
	cause   error
}

func (e *APIError) Error() string {
	if e.cause != nil {
		return e.Message + ": " + e.cause.Error()
	}
	return e.Message
}

func (e *APIError) Unwrap() error {
	return e.cause
}

func (e *APIError) WithCause(cause error) *APIError {
	e.cause = cause
	return e
}

func NewError(status int, code ErrorCode, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

func ValidationError(details []string) *APIError {
	return &APIError{
		Status:  http.StatusBadRequest,
		Code:    CodeValidationError,
		Message: "Validation failed.",
		Details: details,
	}
}

func ValidationMessage(message string) *APIError {
	return NewError(http.StatusBadRequest, CodeValidationError, message)
}

func Unauthorized(code ErrorCode, message string) *APIError {
	return NewError(http.StatusUnauthorized, code, message)
}

func Forbidden(message string) *APIError {
	return NewError(http.StatusForbidden, CodeForbidden, message)
}

func NotFound() *APIError {
	return NotFoundMessage("Resource not found.")
}

// NotFoundMessage is NotFound with the caller's domain wording, because the
// legacy reference names the missing resource in every 404 body.
func NotFoundMessage(message string) *APIError {
	return NewError(http.StatusNotFound, CodeNotFound, message)
}

func Conflict(code ErrorCode, message string) *APIError {
	return NewError(http.StatusConflict, code, message)
}

func PayloadTooLarge() *APIError {
	return NewError(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "Request body too large.")
}

func TooManyRequests(message string) *APIError {
	return NewError(http.StatusTooManyRequests, CodeRateLimited, message)
}

func Internal(cause error) *APIError {
	return &APIError{
		Status:  http.StatusInternalServerError,
		Code:    CodeInternalError,
		Message: "An unexpected error occurred.",
		cause:   cause,
	}
}

type successEnvelope struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

type errorBody struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
}

type errorEnvelope struct {
	Success bool      `json:"success"`
	Error   errorBody `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	payload, err := json.Marshal(successEnvelope{Success: true, Data: data})
	if err != nil {
		payload = []byte(`{"success":true,"data":null}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func StatusForError(err error) (int, ErrorCode, string, any) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status, apiErr.Code, apiErr.Message, apiErr.Details
	}
	return http.StatusInternalServerError, CodeInternalError, "An unexpected error occurred.", nil
}

func WriteError(w http.ResponseWriter, err error) {
	status, code, message, details := StatusForError(err)
	payload, marshalErr := json.Marshal(errorEnvelope{
		Success: false,
		Error:   errorBody{Code: code, Message: message, Details: details},
	})
	if marshalErr != nil {
		payload = []byte(`{"success":false,"error":{"code":"INTERNAL_ERROR","message":"An unexpected error occurred."}}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
