package wal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWAL_OpenFailsWhenRecoveredDirectoryCannotBeSynced(t *testing.T) {
	const childDir = "WAL_RECOVERY_DIR_SYNC_TEST"
	if dir := os.Getenv(childDir); dir != "" {
		debug.SetGCPercent(-1) // A file finalizer must not hide a leaked descriptor.
		f, err := os.Open(os.DevNull)
		require.NoError(t, err)
		var limit syscall.Rlimit
		require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit))
		// Linux ReadDir uses a single descriptor. Once f is closed, one
		// slot is available for each scan or file open, but not for
		// SyncDir while Open is still holding the active file.
		limit.Cur = uint64(f.Fd()) + 1
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit))
		require.NoError(t, f.Close())

		w, err := Open(dir, SyncFsync)
		require.Nil(t, w)
		require.ErrorIs(t, err, syscall.EMFILE)
		var pathErr *os.PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "open", pathErr.Op)
		require.Equal(t, dir, pathErr.Path)

		// This only fits if the failed Open closed the active descriptor.
		w, err = Open(dir, SyncNone)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "00000000000000000001.wal"), []byte(magic), 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWAL_OpenFailsWhenRecoveredDirectoryCannotBeSynced$")
	cmd.Env = append(os.Environ(), childDir+"="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}
