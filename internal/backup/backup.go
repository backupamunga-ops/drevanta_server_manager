// Package backup takes nightly consistent snapshots of the SQLite
// database with `VACUUM INTO` (atomic, includes WAL content) and keeps
// the newest `keep` files under <data-dir>/backups.
//
// Files are created with the daemon's own umask (UMask=0027 → 0640) in
// a directory only the drevanta user can read, so the snapshots are no
// wider than the database itself. Restoring: stop the units, copy the
// snapshot over drevanta.db, start the units.
package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

const (
	firstDelay = 20 * time.Second
	interval   = 24 * time.Hour
)

// Loop runs until ctx is cancelled: one snapshot shortly after start-up
// (so a fresh install has one from day one) and then one per day.
func Loop(ctx context.Context, st *store.Store, dataDir string, keep int) {
	dir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		slog.Error("backup: could not create directory", "dir", dir, "err", err)
		return
	}
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			path, err := snapshot(st, dir)
			if err != nil {
				slog.Error("backup: snapshot failed", "err", err)
			} else {
				slog.Info("backup: snapshot written", "path", path)
				prune(dir, keep)
			}
			timer.Reset(interval)
		}
	}
}

func snapshot(st *store.Store, dir string) (string, error) {
	name := "drevanta-" + time.Now().UTC().Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	if err := st.BackupTo(context.Background(), path); err != nil {
		return "", err
	}
	return path, nil
}

// prune removes the oldest snapshots beyond keep.
func prune(dir string, keep int) {
	if keep < 1 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "drevanta-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names) // timestamped names sort chronologically
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			slog.Warn("backup: prune failed", "file", name, "err", err)
		}
	}
}
