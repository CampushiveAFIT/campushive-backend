package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/logging"
	"campus-hive/backend-go/internal/migrations"
	"campus-hive/backend-go/internal/ratelimit"
	"campus-hive/backend-go/internal/redisx"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx = logging.WithLogger(ctx, logger)

	rootCtx, cancelRoot := context.WithCancel(ctx)
	defer cancelRoot()

	if cfg.AutoMigrate {
		version, err := migrations.Up(rootCtx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
		logger.Info("migrations applied", "version", version)
	}

	db, err := database.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	redisClient, err := redisx.New(rootCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			logger.Warn("redis close failed", "error", closeErr.Error())
		}
	}()

	limiter := ratelimit.New(redisClient.Redis(), logger)

	server := httpserver.New(httpserver.Options{
		Config:         cfg,
		Logger:         logger,
		DB:             db,
		Redis:          redisClient,
		Limiter:        limiter,
		RegisterRoutes: registerRoutes,
	})

	return server.Run(rootCtx)
}
