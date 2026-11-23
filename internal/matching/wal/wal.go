// Package wal implements the matching engine's single-writer segmented log.
package wal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linxun2025/exchange-project/internal/matching/disk"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
)

type SyncMode uint8

const (
	SyncFsync SyncMode = iota + 1
	SyncNone
)

type RecordType uint8

const (
	RecordSubmit RecordType = iota + 1
	RecordCancel
)

// Record.Payload is valid until the next call to the replay handler.
type Record struct {
	LSN     uint64
	Type    RecordType
	Payload []byte
}

const (
	magic         = "EXWAL002"
	baseFrameSize = 17                // uint32 length, uint32 CRC32C, uint64 LSN, byte type
	frameSize     = baseFrameSize + 4 // followed by CRC32C of the base header
	maxPayload    = 1 << 20
	maxSegment    = 64 << 20
	bufferBytes   = 64 << 10
	segmentSuffix = ".wal"
)

var (
	ErrCorrupt = errors.New("wal: corrupt")
	crcTable   = crc32.MakeTable(crc32.Castagnoli)
)

type segment struct {
	name  string
	first uint64
	last  uint64
}

// Log belongs to one actor. Callers serialize all operations, including close.
type Log struct {
	dir          string
	mode         SyncMode
	segments     []segment
	file         *os.File
	writer       *bufio.Writer
	size         int64
	segmentBytes int64
	last         uint64
}

func SanitizeSymbol(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "/", "_"), ":", "_")
}

// Open scans all retained segments and repairs only a torn record at the end
// of the final segment. A complete frame with a bad checksum is corruption.
func Open(dir string, mode SyncMode) (*Log, error) {
	if mode != SyncFsync && mode != SyncNone {
		return nil, fmt.Errorf("invalid wal sync mode %d", mode)
	}
	if err := disk.EnsureDir(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	l := &Log{dir: dir, mode: mode, segmentBytes: maxSegment}
	for _, f := range files {
		name, ok := strings.CutSuffix(f.Name(), segmentSuffix)
		if !ok {
			continue
		}
		first, err := strconv.ParseUint(name, 10, 64)
		if err != nil || first == 0 || len(name) != 20 || !f.Type().IsRegular() {
			return nil, fmt.Errorf("%w: invalid segment %q", ErrCorrupt, f.Name())
		}
		l.segments = append(l.segments, segment{name: f.Name(), first: first})
	}
	if len(l.segments) == 0 {
		if err := l.createSegment(1); err != nil {
			return nil, err
		}
		return l, nil
	}

	reader := newSegmentReader()
	for i := range l.segments {
		s := &l.segments[i]
		if i > 0 && (l.last == math.MaxUint64 || s.first != l.last+1) {
			return nil, fmt.Errorf("%w: discontinuity before segment %s after LSN %d", ErrCorrupt, s.name, l.last)
		}
		active := i == len(l.segments)-1
		path := filepath.Join(dir, s.name)
		if active {
			l.file, err = os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
			if err != nil {
				return nil, err
			}
			l.last, l.size, err = reader.scan(l.file, s.first, true, 0, nil)
			if err != nil {
				return nil, errors.Join(err, l.file.Close())
			}
			if mode == SyncFsync {
				// In SyncFsync mode, rotation syncs each sealed segment
				// before publishing its successor.
				// The active segment may still have unsynced records or a
				// pending rename from the previous process. Persist both
				// (including any tail truncation) before replay or writes.
				if err := l.file.Sync(); err != nil {
					return nil, errors.Join(err, l.file.Close())
				}
				if err := disk.SyncDir(dir); err != nil {
					return nil, errors.Join(err, l.file.Close())
				}
			}
			l.writer = bufio.NewWriterSize(l.file, bufferBytes)
		} else {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			l.last, _, err = reader.scan(f, s.first, false, 0, nil)
			if err := errors.Join(err, f.Close()); err != nil {
				return nil, err
			}
			if l.last < s.first {
				return nil, fmt.Errorf("%w: empty sealed segment %s", ErrCorrupt, s.name)
			}
		}
		s.last = l.last
	}
	return l, nil
}

func (l *Log) createSegment(first uint64) error {
	name := fmt.Sprintf("%020d%s", first, segmentSuffix)
	f, err := os.CreateTemp(l.dir, ".wal-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := l.publishSegment(f, filepath.Join(l.dir, name)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	l.file = f
	if l.writer == nil {
		l.writer = bufio.NewWriterSize(f, bufferBytes)
	} else {
		l.writer.Reset(f)
	}
	l.size = int64(len(magic))
	l.segments = append(l.segments, segment{name: name, first: first, last: first - 1})
	return nil
}

func (l *Log) publishSegment(f *os.File, path string) error {
	if _, err := f.WriteString(magic); err != nil {
		return err
	}
	if l.mode == SyncFsync {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return disk.SyncDir(l.dir)
}

func (l *Log) LastLSN() uint64 { return l.last }

func (l *Log) Append(t RecordType, payload []byte) (uint64, error) {
	if l.file == nil {
		return 0, os.ErrClosed
	}
	if !validType(t) || len(payload) > maxPayload || l.last == math.MaxUint64 {
		return 0, fmt.Errorf("wal: cannot append record type %d, size %d after LSN %d", t, len(payload), l.last)
	}
	lsn := l.last + 1
	if l.size >= l.segmentBytes {
		if err := l.Sync(); err != nil {
			return 0, err
		}
		err := l.file.Close()
		l.file = nil
		if err != nil {
			return 0, err
		}
		if err := l.createSegment(lsn); err != nil {
			return 0, err
		}
	}

	var frame [frameSize]byte
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	binary.BigEndian.PutUint64(frame[8:16], lsn)
	frame[16] = byte(t)
	binary.BigEndian.PutUint32(frame[4:8], checksum(frame[8:baseFrameSize], payload))
	binary.BigEndian.PutUint32(frame[baseFrameSize:], crc32.Checksum(frame[:baseFrameSize], crcTable))
	if _, err := l.writer.Write(frame[:]); err != nil {
		return 0, err
	}
	if _, err := l.writer.Write(payload); err != nil {
		return 0, err
	}
	l.last = lsn
	l.size += int64(len(frame) + len(payload))
	l.segments[len(l.segments)-1].last = lsn
	return lsn, nil
}

func checksum(header, payload []byte) uint32 {
	return crc32.Update(crc32.Checksum(header, crcTable), crcTable, payload)
}

func validType(t RecordType) bool { return t == RecordSubmit || t == RecordCancel }

func (l *Log) Sync() error {
	if l.file == nil {
		return os.ErrClosed
	}
	if err := l.writer.Flush(); err != nil {
		return err
	}
	if l.mode == SyncFsync {
		return l.file.Sync()
	}
	return nil
}

func (l *Log) Close() error {
	if l.file == nil {
		return nil
	}
	err := errors.Join(l.Sync(), l.file.Close())
	l.file = nil
	return err
}

// Replay reads durable records after after in LSN order.
func (l *Log) Replay(after uint64, fn func(Record) error) error {
	if after < l.segments[0].first-1 || after > l.last {
		return fmt.Errorf("%w: cannot replay after LSN %d from retained range [%d, %d]", ErrCorrupt, after, l.segments[0].first, l.last)
	}
	if after == l.last {
		return nil
	}
	reader := newSegmentReader()
	for _, s := range l.segments {
		if s.last <= after {
			continue
		}
		f, err := os.Open(filepath.Join(l.dir, s.name))
		if err != nil {
			return err
		}
		last, _, err := reader.scan(f, s.first, false, after, fn)
		if err := errors.Join(err, f.Close()); err != nil {
			return err
		}
		if last != s.last {
			return fmt.Errorf("%w: segment %s changed while reopening for replay", ErrCorrupt, s.name)
		}
	}
	return nil
}

// Compact removes only a contiguous prefix of sealed segments.
func (l *Log) Compact(upTo uint64) error {
	for len(l.segments) > 1 && l.segments[0].last <= upTo {
		path := filepath.Join(l.dir, l.segments[0].name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Persist each prefix deletion before starting the next. After a
		// crash, discovery must never see a hole between surviving segments.
		if err := disk.SyncDir(l.dir); err != nil {
			return err
		}
		l.segments = l.segments[1:]
	}
	return nil
}

type segmentReader struct {
	r       *bufio.Reader
	payload []byte
}

func newSegmentReader() *segmentReader {
	return &segmentReader{r: bufio.NewReaderSize(nil, bufferBytes)}
}

func (r *segmentReader) scan(f *os.File, first uint64, active bool, after uint64, fn func(Record) error) (uint64, int64, error) {
	r.r.Reset(f)
	var header [len(magic)]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return 0, 0, corrupt(f, 0, fmt.Errorf("segment header: %w", err))
	}
	if string(header[:]) != magic {
		return 0, 0, corrupt(f, 0, errors.New("bad segment magic"))
	}
	last, offset := first-1, int64(len(header))
	var frame [frameSize]byte
	for {
		if _, err := io.ReadFull(r.r, frame[:]); err != nil {
			if err == io.EOF {
				return last, offset, nil
			}
			return last, offset, tailError(f, offset, active, err)
		}
		// Do not trust the length until the header is validated. A flipped
		// length could otherwise hide complete frames behind a fake torn tail.
		if crc32.Checksum(frame[:baseFrameSize], crcTable) != binary.BigEndian.Uint32(frame[baseFrameSize:]) {
			return 0, 0, corrupt(f, offset, errors.New("record header checksum mismatch"))
		}
		length := binary.BigEndian.Uint32(frame[:4])
		lsn := binary.BigEndian.Uint64(frame[8:16])
		t := RecordType(frame[16])
		if length > maxPayload || last == math.MaxUint64 || lsn != last+1 || !validType(t) {
			return 0, 0, corrupt(f, offset, fmt.Errorf("invalid record header: length %d, LSN %d, type %d", length, lsn, t))
		}
		if n := int(length); cap(r.payload) < n {
			r.payload = make([]byte, min(maxPayload, max(n, 2*cap(r.payload))))
		}
		payload := r.payload[:int(length)]
		if _, err := io.ReadFull(r.r, payload); err != nil {
			return last, offset, tailError(f, offset, active, err)
		}
		if checksum(frame[8:baseFrameSize], payload) != binary.BigEndian.Uint32(frame[4:8]) {
			return 0, 0, corrupt(f, offset, errors.New("record checksum mismatch"))
		}
		if fn != nil && lsn > after {
			if err := fn(Record{LSN: lsn, Type: t, Payload: payload}); err != nil {
				return 0, 0, corrupt(f, offset, fmt.Errorf("apply LSN %d: %w", lsn, err))
			}
		}
		last = lsn
		offset += int64(len(frame)) + int64(length)
	}
}

func tailError(f *os.File, offset int64, active bool, err error) error {
	if !active || (err != io.EOF && err != io.ErrUnexpectedEOF) {
		return corrupt(f, offset, err)
	}
	if err := f.Truncate(offset); err != nil {
		return err
	}
	logger.Warn("truncated torn WAL tail", zap.String("path", f.Name()), zap.Int64("offset", offset))
	return nil
}

func corrupt(f *os.File, offset int64, err error) error {
	return fmt.Errorf("%w: %s at offset %d: %w", ErrCorrupt, f.Name(), offset, err)
}
