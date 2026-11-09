package httpx

import (
	"errors"
	"fmt"
)

func wrap(err error) error {
	return fmt.Errorf("context: %w", err)
}

func errorCode(err error) ErrorCode {
	_, code, _, _ := StatusForError(err)
	return code
}

func errorStatus(err error) int {
	status, _, _, _ := StatusForError(err)
	return status
}

func isPayloadTooLarge(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == CodePayloadTooLarge && apiErr.Status == 413
}
