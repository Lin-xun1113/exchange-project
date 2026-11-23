package main

import (
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/stretchr/testify/require"
)

func TestMatchingConfig_Env(t *testing.T) {
	for _, key := range []string{"WAL_DIR", "SNAPSHOT_DIR", "WAL_SYNC", "SNAPSHOT_EVERY", "SNAPSHOT_INTERVAL"} {
		t.Setenv(key, "")
	}
	cfg, err := matchingConfig(nil, nil)
	require.NoError(t, err)
	require.Equal(t, "data/wal", cfg.WALDir)
	require.Equal(t, "data/snapshot", cfg.SnapshotDir)
	require.Equal(t, wal.SyncFsync, cfg.Sync)
	require.EqualValues(t, 50_000, cfg.SnapshotEvery)
	require.Equal(t, time.Minute, cfg.SnapshotInterval)

	t.Setenv("WAL_DIR", "/wal")
	t.Setenv("SNAPSHOT_DIR", "/snap")
	t.Setenv("WAL_SYNC", "none")
	t.Setenv("SNAPSHOT_EVERY", "25")
	t.Setenv("SNAPSHOT_INTERVAL", "5s")
	cfg, err = matchingConfig([]string{"ETH/USDT"}, nil)
	require.NoError(t, err)
	require.Equal(t, "/wal", cfg.WALDir)
	require.Equal(t, "/snap", cfg.SnapshotDir)
	require.Equal(t, wal.SyncNone, cfg.Sync)
	require.EqualValues(t, 25, cfg.SnapshotEvery)
	require.Equal(t, 5*time.Second, cfg.SnapshotInterval)
	require.Equal(t, []string{"ETH/USDT"}, cfg.Symbols)
}

func TestMatchingConfig_InvalidEnv(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"WAL_SYNC", "byduration"}, {"SNAPSHOT_EVERY", "-1"}, {"SNAPSHOT_EVERY", "0"},
		{"SNAPSHOT_INTERVAL", "5"}, {"SNAPSHOT_INTERVAL", "-1s"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv("WAL_SYNC", "fsync")
			t.Setenv("SNAPSHOT_EVERY", "50000")
			t.Setenv("SNAPSHOT_INTERVAL", "60s")
			t.Setenv(tc.key, tc.value)
			_, err := matchingConfig(nil, nil)
			require.ErrorContains(t, err, tc.key)
		})
	}
}
