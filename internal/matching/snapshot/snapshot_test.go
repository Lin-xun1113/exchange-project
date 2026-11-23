package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLatestFallsBackAndPruneKeepsTwoValidSnapshots(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "snapshot")
	lsn, body, err := Latest(dir)
	require.NoError(t, err)
	require.Zero(t, lsn)
	require.Nil(t, body)

	require.NoError(t, Write(dir, 1, []byte("one")))
	oldest, err := Prune(dir, 2)
	require.NoError(t, err)
	require.Zero(t, oldest, "no safe compaction point without a fallback")
	require.NoError(t, Write(dir, 2, []byte("two")))
	corrupt(t, dir, "00000000000000000002.snap")
	lsn, body, err = Latest(dir)
	require.NoError(t, err)
	require.EqualValues(t, 1, lsn)
	require.Equal(t, []byte("one"), body)

	require.NoError(t, Write(dir, 3, []byte("three")))
	oldest, err = Prune(dir, 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, oldest)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "00000000000000000001.snap", files[0].Name())
	require.Equal(t, "00000000000000000003.snap", files[1].Name())
}

func TestLatestFailsClosedIfAllSnapshotsAreCorrupt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Write(dir, 1, []byte("one")))
	corrupt(t, dir, "00000000000000000001.snap")
	_, _, err := Latest(dir)
	require.Error(t, err)
}

func TestSymlinkDoesNotCountAsSnapshotFallback(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Write(dir, 1, []byte("one")))
	require.NoError(t, os.Symlink("00000000000000000001.snap", filepath.Join(dir, "00000000000000000002.snap")))
	lsn, body, err := Latest(dir)
	require.NoError(t, err)
	require.EqualValues(t, 1, lsn)
	require.Equal(t, []byte("one"), body)
	oldest, err := Prune(dir, 2)
	require.NoError(t, err)
	require.Zero(t, oldest)
}

func TestPrunePreservesSnapshotOnOperationalReadError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Write(dir, 1, []byte("one")))
	require.NoError(t, Write(dir, 2, []byte("two")))
	require.NoError(t, Write(dir, 3, []byte("three")))
	path := filepath.Join(dir, "00000000000000000002.snap")
	require.NoError(t, os.Chmod(path, 0))
	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		t.Skip("current user can read mode-000 files")
	}
	require.ErrorIs(t, err, os.ErrPermission)
	oldest, err := Prune(dir, 2)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Zero(t, oldest)
	require.FileExists(t, path)
	require.FileExists(t, filepath.Join(dir, "00000000000000000001.snap"))
}

func corrupt(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data[8] ^= 1 // Corrupt the CRC in the header while leaving the body unchanged.
	require.NoError(t, os.WriteFile(path, data, 0600))
}
