package httpx

import (
	"fmt"
	"net/http"
)

func RouteNotFound(r *http.Request) *APIError {
	return NewError(
		http.StatusNotFound,
		CodeNotFound,
		fmt.Sprintf("Cannot %s %s", r.Method, r.URL.Path),
	)
}

func HandlerNotFound() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, RouteNotFound(r))
	}
}

// HandlerMethodNotAllowed mirrors Nest: a known path with an unsupported
// method is a 404, not a 405, and the message names the requested method.
func HandlerMethodNotAllowed() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, RouteNotFound(r))
	}
}
