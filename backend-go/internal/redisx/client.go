package redisx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"campus-hive/backend-go/internal/config"

	"github.com/redis/go-redis/v9"
)

const readyTimeout = 30 * time.Second

type Client struct {
	rdb      *redis.Client
	embedded *embeddedServer
}

type embeddedServer struct {
	cmd     *exec.Cmd
	dataDir string
}

func (s *embeddedServer) stop() {
	if s == nil {
		return
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	if s.dataDir != "" {
		_ = os.RemoveAll(s.dataDir)
	}
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Client, error) {
	if cfg.RedisURL != "" {
		logger.Info("connecting to redis via REDIS_URL")
		rdb := newRedisClient(cfg.RedisURL)
		if err := waitReady(ctx, rdb, readyTimeout); err != nil {
			_ = rdb.Close()
			return nil, fmt.Errorf("redis connection failed: %w", err)
		}
		logger.Info("redis connection established (external)")
		return &Client{rdb: rdb}, nil
	}

	if cfg.IsProduction() {
		return nil, errors.New("REDIS_URL is required in production. Refusing to start without rate-limit storage.")
	}

	binary, err := resolveServerBinary(cfg.RedisServerBinary)
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	dataDir, err := os.MkdirTemp("", "campus-hive-redis-")
	if err != nil {
		return nil, fmt.Errorf("create redis data dir: %w", err)
	}
	logger.Warn("REDIS_URL unset: starting embedded redis server (development/test only)", "binary", binary, "port", port)
	cmd := exec.Command(binary,
		"--port", strconv.Itoa(port),
		"--bind", "127.0.0.1",
		"--save", "",
		"--appendonly", "no",
		"--dir", dataDir,
		"--daemonize", "no",
		"--protected-mode", "yes",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("start embedded redis server: %w", err)
	}
	embedded := &embeddedServer{cmd: cmd, dataDir: dataDir}

	rdb := newRedisClient(fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	if err := waitReady(ctx, rdb, readyTimeout); err != nil {
		_ = rdb.Close()
		embedded.stop()
		return nil, fmt.Errorf("embedded redis did not become ready: %w", err)
	}
	logger.Info("embedded redis ready", "addr", fmt.Sprintf("127.0.0.1:%d", port))
	return &Client{rdb: rdb, embedded: embedded}, nil
}

func NewForTest(ctx context.Context, addr string) (*Client, error) {
	rdb := newRedisClient(addr)
	if err := waitReady(ctx, rdb, readyTimeout); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &Client{rdb: rdb}, nil
}

func newRedisClient(rawURL string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:                  addrFromURL(rawURL),
		Username:              usernameFromURL(rawURL),
		Password:              passwordFromURL(rawURL),
		DB:                    dbFromURL(rawURL),
		MaxRetries:            2,
		DisableIdentity:       true,
		ConnMaxIdleTime:       5 * time.Minute,
		ContextTimeoutEnabled: true,
	})
}

func (c *Client) Redis() *redis.Client {
	return c.rdb
}

func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client is not initialised")
	}
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	var err error
	if c.rdb != nil {
		err = c.rdb.Close()
	}
	c.embedded.stop()
	return err
}

func waitReady(ctx context.Context, rdb *redis.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return lastErr
}

func resolveServerBinary(configured string) (string, error) {
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "bin", "redis-server"),
			filepath.Join(dir, "..", "bin", "redis-server"),
			filepath.Join(dir, "..", "backend", "bin", "redis-server"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "bin", "redis-server"),
			filepath.Join(wd, "..", "backend", "bin", "redis-server"),
		)
	}
	if path, err := exec.LookPath("redis-server"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return candidate, nil
			}
			return abs, nil
		}
	}
	return "", errors.New("no REDIS_URL set and no redis-server binary found; set REDIS_URL or REDIS_SERVER_BINARY")
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func addrFromURL(rawURL string) string {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return rawURL
	}
	return opts.Addr
}

func usernameFromURL(rawURL string) string {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return ""
	}
	return opts.Username
}

func passwordFromURL(rawURL string) string {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return ""
	}
	return opts.Password
}

func dbFromURL(rawURL string) int {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return 0
	}
	return opts.DB
}
