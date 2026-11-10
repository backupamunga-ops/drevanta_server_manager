// drevantad is the Drevanta server management daemon.
//
// Sprint 1 walking skeleton: a hardened, unprivileged HTTP daemon that
// exposes a frozen API surface (GET /healthz, GET /readyz,
// GET /api/v1/server/metrics) plus the embedded SvelteKit dashboard, and
// records lifecycle events into a local SQLite audit log.
//
// Design invariants:
//   - Loopback-only by default: the only network surface is 127.0.0.1.
//   - Runs unprivileged: no capabilities, no root, no Docker/Nginx helpers.
//   - Metrics are collected on demand from /proc; there is no background
//     sampling loop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpapi"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// version is stamped via -ldflags "-X main.version=..." in release builds.
var version = "0.1.0-sprint1"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (loopback only by design)")
	dataDir := flag.String("data-dir", "/var/lib/drevanta", "state directory holding the SQLite database")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(*addr, *dataDir); err != nil {
		slog.Error("drevantad: fatal", "err", err)
		os.Exit(1)
	}
}

func run(addr, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", dataDir, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(dataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate store: %w", err)
	}

	// Lifecycle events go to the audit log. The startup write uses a
	// detached context so it cannot be dropped by shutdown cancellation.
	_ = st.Audit(context.WithoutCancel(ctx), "drevantad", "daemon_started", "daemon", map[string]any{
		"version":  version,
		"pid":      os.Getpid(),
		"addr":     addr,
		"data_dir": dataDir,
	})
	defer func() {
		_ = st.Audit(context.Background(), "drevantad", "daemon_stopped", "daemon", map[string]any{"version": version})
	}()

	diskPaths := dedupe([]string{"/", dataDir})
	mux := httpapi.New(st, version, diskPaths)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	slog.Info("drevantad listening", "addr", addr, "version", version, "data_dir", dataDir)

	select {
	case <-ctx.Done():
		slog.Info("drevantad shutting down")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
