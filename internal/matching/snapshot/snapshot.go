// Package snapshot stores and loads checksummed, atomically published snapshots.
package snapshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linxun2025/exchange-project/internal/matching/disk"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
)

const (
	magic      = "EXSNAP01"
	headerSize = len(magic) + 4 + 8
	suffix     = ".snap"
)

var (
	crcTable   = crc32.MakeTable(crc32.Castagnoli)
	errCorrupt = errors.New("corrupt snapshot")
)

func Write(dir string, lsn uint64, body []byte) error {
	if err := disk.EnsureDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	var header [headerSize]byte
	copy(header[:], magic)
	binary.BigEndian.PutUint32(header[8:12], crc32.Checksum(body, crcTable))
	binary.BigEndian.PutUint64(header[12:20], uint64(len(body)))
	if _, err = f.Write(header[:]); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, fmt.Sprintf("%020d%s", lsn, suffix))); err != nil {
		return err
	}
	return disk.SyncDir(dir)
}

// Latest returns the latest valid snapshot, falling back on corruption.
// It returns a startup error if there are snapshots but none are valid.
func Latest(dir string) (lsn uint64, body []byte, err error) {
	files, err := candidates(dir)
	if err != nil || len(files) == 0 {
		return 0, nil, err
	}
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		lsn, body, err = read(path, true)
		if err == nil {
			return lsn, body, nil
		}
		if !errors.Is(err, errCorrupt) {
			return 0, nil, fmt.Errorf("read snapshot %s: %w", path, err)
		}
		logger.Warn("skipping invalid snapshot", zap.String("path", path), zap.Error(err))
	}
	return 0, nil, fmt.Errorf("no valid snapshot in %s: %w", dir, err)
}

// Prune keeps the latest keep valid snapshots. It returns a safe WAL
// compaction boundary only when all keep snapshots are available.
func Prune(dir string, keep int) (uint64, error) {
	if keep < 1 {
		return 0, errors.New("must keep at least one snapshot")
	}
	files, err := candidates(dir)
	if err != nil {
		return 0, err
	}
	var oldest uint64
	var kept int
	var remove []string
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		if kept < keep {
			lsn, _, readErr := read(path, false)
			if readErr == nil {
				oldest = lsn
				kept++
				continue
			}
			if !errors.Is(readErr, errCorrupt) {
				return 0, fmt.Errorf("read snapshot %s: %w", path, readErr)
			}
		}
		remove = append(remove, path)
	}
	for i, path := range remove {
		if err := os.Remove(path); err != nil {
			if i > 0 {
				err = errors.Join(err, disk.SyncDir(dir))
			}
			return 0, err
		}
	}
	if len(remove) > 0 {
		err = disk.SyncDir(dir)
	}
	if err != nil || kept < keep {
		return 0, err
	}
	return oldest, nil
}

func candidates(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []os.DirEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if strings.HasSuffix(entries[i].Name(), suffix) {
			files = append(files, entries[i])
		}
	}
	return files, nil
}

func read(path string, loadBody bool) (uint64, []byte, error) {
	name := strings.TrimSuffix(filepath.Base(path), suffix)
	lsn, err := strconv.ParseUint(name, 10, 64)
	if err != nil || len(name) != 20 {
		return 0, nil, fmt.Errorf("%w: invalid snapshot filename", errCorrupt)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < int64(headerSize) {
		return 0, nil, fmt.Errorf("%w: invalid snapshot file", errCorrupt)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	var header [headerSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, nil, readError(err)
	}
	length := binary.BigEndian.Uint64(header[12:20])
	if string(header[:8]) != magic || length != uint64(info.Size()-int64(headerSize)) {
		return 0, nil, fmt.Errorf("%w: invalid snapshot header", errCorrupt)
	}
	var body []byte
	var crc uint32
	if loadBody {
		if length > uint64(^uint(0)>>1) {
			return 0, nil, errors.New("snapshot length overflows int")
		}
		body = make([]byte, int(length))
		if _, err := io.ReadFull(f, body); err != nil {
			return 0, nil, readError(err)
		}
		crc = crc32.Checksum(body, crcTable)
	} else {
		hash := crc32.New(crcTable)
		if _, err := io.CopyN(hash, f, int64(length)); err != nil {
			return 0, nil, readError(err)
		}
		crc = hash.Sum32()
	}
	if crc != binary.BigEndian.Uint32(header[8:12]) {
		return 0, nil, fmt.Errorf("%w: checksum mismatch", errCorrupt)
	}
	return lsn, body, nil
}

func readError(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return fmt.Errorf("%w: truncated file: %v", errCorrupt, err)
	}
	return err
}
