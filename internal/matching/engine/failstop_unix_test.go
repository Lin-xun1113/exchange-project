//go:build darwin || linux

package engine

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/stretchr/testify/require"
)

func TestMatcher_FailStopsOnWALBatchFlushError(t *testing.T) {
	if dir := os.Getenv("MATCHING_FAIL_STOP_FLUSH_TEST"); dir != "" {
		cfg := Config{WALDir: filepath.Join(dir, "wal"), SnapshotDir: filepath.Join(dir, "snap"), Symbols: []string{market}}
		m := openMatcher(t, cfg)
		info, err := os.Stat(segment(symbolDir(cfg.WALDir, market), 1))
		require.NoError(t, err)
		// Prevent any real file writes beyond the segment header. A normal
		// order fits in bufio, so the kernel error occurs during batch Sync.
		signal.Ignore(syscall.SIGXFSZ)
		var limit syscall.Rlimit
		require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit))
		limit.Cur = uint64(info.Size())
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit))
		_, err = m.Submit(t.Context(), market, order("maker", book.Sell, book.Limit, 11, 1))
		require.ErrorIs(t, err, ErrClosed)
		<-time.After(3 * time.Second)
		t.Fatal("actor continued running after WAL flush failure")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMatcher_FailStopsOnWALBatchFlushError$")
	cmd.Env = append(os.Environ(), "MATCHING_FAIL_STOP_FLUSH_TEST="+t.TempDir())
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", out)
	require.Equal(t, 1, exit.ExitCode(), "%s", out)
	require.Contains(t, string(out), `"level":"fatal"`, "%s", out)
	require.Contains(t, string(out), "wal sync failed", "%s", out)
}
