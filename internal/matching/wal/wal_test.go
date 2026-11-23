package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWAL_TornTail(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, SyncFsync)
	require.NoError(t, err)
	for i := uint64(1); i <= 3; i++ {
		lsn, err := w.Append(RecordSubmit, []byte(`{"o":"order"}`))
		require.NoError(t, err)
		require.Equal(t, i, lsn)
	}
	require.NoError(t, w.Sync())
	require.NoError(t, w.Close())

	path := filepath.Join(dir, "00000000000000000001.wal")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, os.Truncate(path, info.Size()-5))

	w, err = Open(dir, SyncFsync)
	require.NoError(t, err)
	require.EqualValues(t, 2, w.LastLSN())
	var got []uint64
	require.NoError(t, w.Replay(0, func(r Record) error {
		got = append(got, r.LSN)
		return nil
	}))
	require.Equal(t, []uint64{1, 2}, got)
	lsn, err := w.Append(RecordCancel, []byte(`{"o":"order"}`))
	require.NoError(t, err)
	require.EqualValues(t, 3, lsn)
	require.NoError(t, w.Close())
	w, err = Open(dir, SyncFsync)
	require.NoError(t, err)
	require.EqualValues(t, 3, w.LastLSN())
	require.NoError(t, w.Close())
}

func TestWAL_RotatedCorruption(t *testing.T) {
	for _, tt := range []struct {
		name   string
		file   int
		damage func([]byte) []byte
	}{
		{"crc in middle segment", 1, func(data []byte) []byte { data[len(data)-1] ^= 1; return data }},
		{"torn middle segment", 1, func(data []byte) []byte { return data[:len(data)-5] }},
		{"crc in active segment", 2, func(data []byte) []byte { data[len(data)-1] ^= 1; return data }},
		{"bad segment header", 2, func(data []byte) []byte { data[0] ^= 1; return data }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w := threeSegments(t, dir)
			require.NoError(t, w.Close())
			files, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, files, 3)
			path := filepath.Join(dir, files[tt.file].Name())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, tt.damage(data), 0600))
			_, err = Open(dir, SyncNone)
			require.ErrorIs(t, err, ErrCorrupt)
		})
	}
}

func TestWAL_LengthCorruptionCannotMaskNextFrame(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, SyncFsync)
	require.NoError(t, err)
	for i := range 3 {
		lsn, err := w.Append(RecordSubmit, []byte{byte(i)})
		require.NoError(t, err)
		require.Equal(t, uint64(i+1), lsn)
	}
	require.NoError(t, w.Close())
	path := filepath.Join(dir, "00000000000000000001.wal")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	// Increase the first record length without changing either later frame.
	binary.BigEndian.PutUint32(data[len(magic):], 64*1024)
	require.NoError(t, os.WriteFile(path, data, 0600))
	_, err = Open(dir, SyncFsync)
	require.ErrorIs(t, err, ErrCorrupt)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, after, "corruption must not truncate acknowledged records")
}

func TestWAL_CompactDeletesOnlySealedSegmentsAndPreservesLSN(t *testing.T) {
	dir := t.TempDir()
	w := threeSegments(t, dir)
	require.NoError(t, w.Compact(3))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "00000000000000000003.wal", files[0].Name())
	require.NoError(t, w.Close())

	w, err = Open(dir, SyncNone)
	require.NoError(t, err)
	require.EqualValues(t, 3, w.LastLSN())
	require.ErrorIs(t, w.Replay(1, func(Record) error { return nil }), ErrCorrupt)
	var got []uint64
	require.NoError(t, w.Replay(2, func(r Record) error {
		got = append(got, r.LSN)
		return nil
	}))
	require.Equal(t, []uint64{3}, got)
	lsn, err := w.Append(RecordCancel, []byte("next"))
	require.NoError(t, err)
	require.EqualValues(t, 4, lsn)
	require.NoError(t, w.Close())
}

func TestWAL_EmptyActiveSegmentMaintainsLSN(t *testing.T) {
	dir := t.TempDir()
	w := threeSegments(t, dir)
	require.NoError(t, w.Compact(2))
	require.NoError(t, w.Close())
	path := filepath.Join(dir, "00000000000000000003.wal")
	require.NoError(t, os.Truncate(path, int64(len(magic))))
	w, err := Open(dir, SyncNone)
	require.NoError(t, err)
	require.EqualValues(t, 2, w.LastLSN())
	lsn, err := w.Append(RecordSubmit, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, lsn)
	require.NoError(t, w.Close())
}

func TestWAL_RejectsSequenceGapAndLSNWrap(t *testing.T) {
	t.Run("segment gap", func(t *testing.T) {
		dir := t.TempDir()
		w := threeSegments(t, dir)
		require.NoError(t, w.Close())
		require.NoError(t, os.Remove(filepath.Join(dir, "00000000000000000002.wal")))
		_, err := Open(dir, SyncNone)
		require.ErrorIs(t, err, ErrCorrupt)
	})
	t.Run("max LSN", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "18446744073709551615.wal"), []byte(magic), 0600))
		w, err := Open(dir, SyncNone)
		require.NoError(t, err)
		lsn, err := w.Append(RecordSubmit, nil)
		require.NoError(t, err)
		require.Equal(t, uint64(math.MaxUint64), lsn)
		_, err = w.Append(RecordSubmit, nil)
		require.Error(t, err)
		require.Equal(t, uint64(math.MaxUint64), w.LastLSN())
		require.NoError(t, w.Close())
		w, err = Open(dir, SyncNone)
		require.NoError(t, err)
		require.NoError(t, w.Replay(math.MaxUint64, func(Record) error { return errors.New("unexpected record") }))
		require.NoError(t, w.Close())
	})
}

func TestWAL_RejectsOversizeHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "00000000000000000001.wal")
	var frame [frameSize]byte
	binary.BigEndian.PutUint32(frame[:4], maxPayload+1)
	binary.BigEndian.PutUint64(frame[8:16], 1)
	frame[16] = byte(RecordSubmit)
	binary.BigEndian.PutUint32(frame[baseFrameSize:], crc32.Checksum(frame[:baseFrameSize], crcTable))
	require.NoError(t, os.WriteFile(path, append([]byte(magic), frame[:]...), 0600))
	_, err := Open(dir, SyncNone)
	require.ErrorIs(t, err, ErrCorrupt)
}

func TestWAL_PropagatesRealFlushAndFileSyncErrors(t *testing.T) {
	for _, unflushed := range []bool{false, true} {
		w, err := Open(t.TempDir(), SyncFsync)
		require.NoError(t, err)
		if unflushed {
			_, err = w.Append(RecordSubmit, []byte("buffered"))
			require.NoError(t, err)
		}
		require.NoError(t, w.file.Close())
		require.ErrorIs(t, w.Sync(), os.ErrClosed)
		require.ErrorIs(t, w.Close(), os.ErrClosed)
	}
}

func threeSegments(t *testing.T, dir string) *Log {
	t.Helper()
	w, err := Open(dir, SyncNone)
	require.NoError(t, err)
	w.segmentBytes = int64(len(magic)) + 1 // Exercise the same roll path as a filled 64 MiB segment.
	for i := uint64(1); i <= 3; i++ {
		lsn, err := w.Append(RecordSubmit, []byte("order"))
		require.NoError(t, err)
		require.Equal(t, i, lsn)
	}
	return w
}
