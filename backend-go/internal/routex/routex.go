// Package routex declares the API prefixes shared by several controllers.
// chi panics when a pattern is mounted twice, and the legacy reference
// registers more than one controller per prefix (/users, /universities,
// /communities, ...), so the prefix and its guard are created once here and
// handed to every controller that contributes routes to it.
package routex

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Authed mounts pattern on parent and applies middleware to every route the
// returned subrouter later receives. Middleware is attached before any route
// exists, so no controller can register an unguarded route by ordering.
func Authed(parent chi.Router, pattern string, middleware ...func(http.Handler) http.Handler) chi.Router {
	subrouter := chi.NewRouter()
	subrouter.Use(middleware...)
	parent.Mount(pattern, subrouter)
	return subrouter
}
