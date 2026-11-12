// Serve mode: the hardened HTTP daemon (Sprint 1 logic, unchanged
// semantics) plus small helpers shared by the claim subcommands.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/backup"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpapi"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

func run(addr, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", dataDir, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

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

	// Opportunistic hygiene: drop expired sessions at start-up.
	_ = st.PruneSessions(ctx)

	// Nightly consistent DB snapshots (VACUUM INTO), last 7 kept.
	go backup.Loop(ctx, st, dataDir, 7)

	diskPaths := dedupe([]string{"/", dataDir})
	// The access view points the access-mode/domain endpoints at the
	// same root-managed configuration and secret-free status files the
	// CLI uses; the overlay directory is where the daemon stores the
	// optional custom-domain request for the httpsd proxy worker.
	p := httpsd.RuntimePathsFromEnv()
	accessView := &httpapi.AccessView{
		ConfigPath:    p.ConfigPath,
		HTTPSStateDir: p.StateDir,
		OverlayDir:    httpsd.OverlayDir(),
	}
	mux := httpapi.NewWithAccess(st, version, diskPaths, accessView)

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

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	return st, nil
}

func slogError(msg string, err error) {
	slog.Error(msg, "err", err)
}

func newToken() (string, string, error) {
	return auth.NewToken()
}

func auditEvent(ctx context.Context, st *store.Store, actor, action, target string, detail map[string]any) {
	if err := st.Audit(ctx, actor, action, target, detail); err != nil {
		slog.Error("audit write failed", "action", action, "err", err)
	}
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
