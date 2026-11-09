package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/logging"
	"campus-hive/backend-go/internal/openapi"
	"campus-hive/backend-go/internal/ratelimit"
	"campus-hive/backend-go/internal/redisx"

	"github.com/go-chi/chi/v5"
)

const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 60 * time.Second
	ShutdownTimeout   = 15 * time.Second
	corsMaxAge        = 10 * time.Minute
)

type Options struct {
	Config         config.Config
	Logger         *slog.Logger
	DB             *database.DB
	Redis          *redisx.Client
	Limiter        *ratelimit.Limiter
	RegisterRoutes func(api chi.Router, deps Dependencies, document *openapi.Document)
}

type Dependencies struct {
	Config  config.Config
	Logger  *slog.Logger
	DB      *database.DB
	Redis   *redisx.Client
	Limiter *ratelimit.Limiter
}

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	router  *chi.Mux
	docs    *openapi.Document
	health  *HealthChecker
	httpSrv *http.Server
}

func New(opts Options) *Server {
	router := chi.NewRouter()
	router.Use(logging.Middleware(opts.Logger))
	router.Use(httpx.RequestID)
	router.Use(httpx.AccessLog(opts.Config.TrustProxy))
	router.Use(httpx.SecurityHeaders)
	router.Use(httpx.CORS(opts.Config.CORSOrigins, corsMaxAge))
	router.Use(httpx.BodyLimit(opts.Config.MaxBodyBytes))
	router.Use(httpx.PreParseBody)
	router.Use(httpx.Recover)

	srv := &Server{
		cfg:    opts.Config,
		logger: opts.Logger,
		router: router,
		docs: openapi.New(
			"Campus Hive API",
			"1.0",
			"Campus Hive backend - university-centered social platform (Slices 1-6)",
		),
		health: &HealthChecker{env: opts.Config.NodeEnv, db: opts.DB, redis: opts.Redis},
	}

	router.Get("/healthz", srv.health.Handle)

	serveDocument := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(srv.docs.JSON())
	}
	router.Get("/api-json", serveDocument)
	router.Get("/api/docs-json", serveDocument)
	router.Get("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		payload, err := srv.docs.HTML("/api/docs-json")
		if err != nil {
			httpx.WriteError(w, httpx.Internal(err))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(payload)
	})

	api := chi.NewRouter()
	if opts.RegisterRoutes != nil {
		opts.RegisterRoutes(api, Dependencies{
			Config:  opts.Config,
			Logger:  opts.Logger,
			DB:      opts.DB,
			Redis:   opts.Redis,
			Limiter: opts.Limiter,
		}, srv.docs)
	}
	router.Mount("/api/v1", api)

	router.NotFound(httpx.HandlerNotFound())
	router.MethodNotAllowed(httpx.HandlerMethodNotAllowed())

	srv.httpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", opts.Config.Port),
		Handler:           router,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
	}
	return srv
}

func (s *Server) Router() *chi.Mux {
	return s.router
}

func (s *Server) Docs() *openapi.Document {
	return s.docs
}

func (s *Server) HTTPAddress() string {
	return s.httpSrv.Addr
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("campus hive go backend listening", "port", s.cfg.Port, "env", s.cfg.NodeEnv)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutdown signal received")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	if err := <-errCh; err != nil {
		return err
	}
	s.logger.Info("server stopped")
	return nil
}

type HealthChecker struct {
	env   string
	db    *database.DB
	redis *redisx.Client
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Redis    string `json:"redis"`
}

func (h *HealthChecker) Handle(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	response := healthResponse{Status: "ok", Database: "up", Redis: "up"}
	statusCode := http.StatusOK
	checkDependencies := h.env != "test"
	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			response.Database = "down"
			response.Status = "degraded"
			statusCode = http.StatusServiceUnavailable
		}
	} else if checkDependencies {
		response.Database = "down"
		response.Status = "degraded"
		statusCode = http.StatusServiceUnavailable
	} else {
		response.Database = "not-configured"
	}
	if h.redis != nil {
		if err := h.redis.Ping(ctx); err != nil {
			response.Redis = "down"
			response.Status = "degraded"
			statusCode = http.StatusServiceUnavailable
		}
	} else if checkDependencies {
		response.Redis = "down"
		response.Status = "degraded"
		statusCode = http.StatusServiceUnavailable
	} else {
		response.Redis = "not-configured"
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(response)
}
