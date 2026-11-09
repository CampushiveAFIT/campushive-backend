package main

import (
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/openapi"
	"campus-hive/backend-go/internal/routes"

	"github.com/go-chi/chi/v5"
)

func registerRoutes(api chi.Router, deps httpserver.Dependencies, document *openapi.Document) {
	routes.Register(api, deps, document)
}
