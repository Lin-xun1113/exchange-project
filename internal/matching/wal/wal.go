package wal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
)

// EntryType represents the type of a WAL entry.
type EntryType uint8

const (
	EntryTypeCommand EntryType = iota
	EntryTypeCancel
	EntryTypeTrade
)

func (t EntryType) String() string {
	switch t {
	case EntryTypeCommand:
		return "command"
	case EntryTypeCancel:
		return "cancel"
	case EntryTypeTrade:
		return "trade"
	default:
		return "unknown"
	}
}

// Entry represents a single WAL record.
type Entry struct {
	LSN     uint64
	Type    EntryType
	Payload interface{}
}

// CommandPayload is the payload for a submit order command.
type CommandPayload struct {
	OrderID   string
	UserID    int64
	Side      string
	OrderType string
	Price     string
	Quantity  string
}

// CancelPayload is the payload for a cancel order command.
type CancelPayload struct {
	OrderID string
}

// TradePayload is the payload for a trade event.
type TradePayload struct {
	TradeID     string
	BuyOrderID  string
	SellOrderID string
	BuyUserID   int64
	SellUserID  int64
	Price       string
	Quantity    string
	Symbol      string
	OrderID     string
}

// WALWriter is the interface for writing to WAL.
type WALWriter interface {
	Append(entry *Entry) (uint64, error)
	LastLSN() uint64
	Close() error
}

// WAL implements an append-only Write-Ahead Log for a single symbol.
type WAL struct {
	mu     sync.Mutex
	symbol string
	path   string
	file   *os.File
	writer *bufio.Writer
	lsn    atomic.Uint64
	closed atomic.Bool
}

// SanitizeSymbol converts a symbol to a safe filename by replacing special characters.
// For example: "BTC/USDT" -> "BTC_USDT"
func SanitizeSymbol(symbol string) string {
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	safeSymbol = strings.ReplaceAll(safeSymbol, ":", "_")
	return safeSymbol
}

// NewWAL opens (or creates) the WAL file for a symbol and returns a WAL instance.
func NewWAL(symbol, dir string) (*WAL, error) {
	// Sanitize symbol for use as filename
	safeSymbol := SanitizeSymbol(symbol)

	fullDir := filepath.Join(dir, safeSymbol)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL dir %s: %w", fullDir, err)
	}

	path := filepath.Join(fullDir, safeSymbol+".wal")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file %s: %w", path, err)
	}

	w := &WAL{
		symbol: symbol,
		path:   path,
		file:   file,
		writer: bufio.NewWriter(file),
	}

	if err := w.readLastLSN(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to read last LSN: %w", err)
	}

	logger.Info("WAL opened",
		logger.S("symbol", symbol),
		logger.S("path", path),
		logger.I64("last_lsn", int64(w.lsn.Load())),
	)

	return w, nil
}

// readLastLSN scans the file to find the last complete record's LSN.
func (w *WAL) readLastLSN() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		w.lsn.Store(0)
		return nil
	}

	fileSize := info.Size()
	if fileSize < 13 {
		w.lsn.Store(0)
		return nil
	}

	// Read the entire file and find the last valid LSN.
	data, err := io.ReadAll(w.file)
	if err != nil {
		w.lsn.Store(0)
		return nil
	}

	var lastLSN uint64
	pos := 0
	for pos+13 <= len(data) {
		lsn := binary.BigEndian.Uint64(data[pos:])
		entryType := data[pos+8]
		if entryType > 2 {
			break
		}
		payloadLen := binary.BigEndian.Uint32(data[pos+9:])
		totalLen := 13 + int(payloadLen)
		if pos+totalLen > len(data) {
			break
		}
		lastLSN = lsn
		pos += totalLen
	}

	w.lsn.Store(lastLSN)
	return nil
}

// Append writes a WAL entry to the file synchronously.
func (w *WAL) Append(entry *Entry) (uint64, error) {
	if w.closed.Load() {
		return 0, fmt.Errorf("WAL is closed")
	}

	start := time.Now()
	defer func() {
		metrics.GetMetrics().RecordWALAppendLatency(time.Since(start))
	}()

	lsn := w.lsn.Add(1)
	entry.LSN = lsn

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	switch p := entry.Payload.(type) {
	case CommandPayload:
		if err := enc.Encode(p); err != nil {
			return 0, fmt.Errorf("failed to encode command payload: %w", err)
		}
	case CancelPayload:
		if err := enc.Encode(p); err != nil {
			return 0, fmt.Errorf("failed to encode cancel payload: %w", err)
		}
	case TradePayload:
		if err := enc.Encode(p); err != nil {
			return 0, fmt.Errorf("failed to encode trade payload: %w", err)
		}
	default:
		return 0, fmt.Errorf("unsupported payload type: %T", entry.Payload)
	}

	payloadBytes := buf.Bytes()
	payloadLen := uint32(len(payloadBytes))

	record := make([]byte, 13+payloadLen)
	binary.BigEndian.PutUint64(record[0:8], lsn)
	record[8] = byte(entry.Type)
	binary.BigEndian.PutUint32(record[9:13], payloadLen)
	copy(record[13:], payloadBytes)

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.writer.Write(record); err != nil {
		return 0, fmt.Errorf("failed to write WAL record: %w", err)
	}

	// Flush on every write to ensure durability for truncation
	if err := w.writer.Flush(); err != nil {
		return lsn, fmt.Errorf("failed to flush WAL: %w", err)
	}

	logger.Debug("WAL append",
		logger.S("symbol", w.symbol),
		logger.I64("lsn", int64(lsn)),
		logger.S("type", entry.Type.String()),
	)

	return lsn, nil
}

// LastLSN returns the LSN of the last record in the WAL file.
func (w *WAL) LastLSN() uint64 {
	return w.lsn.Load()
}

// Close flushes and closes the WAL file.
func (w *WAL) Close() error {
	if w.closed.Swap(true) {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			logger.Error("failed to flush WAL on close",
				logger.S("symbol", w.symbol),
				logger.Err(err),
			)
		}
	}

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("failed to close WAL file: %w", err)
		}
	}

	logger.Info("WAL closed", logger.S("symbol", w.symbol))
	return nil
}

// Replay reads WAL entries from fromLSN (exclusive) to the end, calling handler for each.
func (w *WAL) Replay(fromLSN uint64, handler func(*Entry) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	file, err := os.Open(w.path)
	if err != nil {
		return fmt.Errorf("failed to open WAL for replay: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	for {
		header := make([]byte, 13)
		_, err := reader.Read(header)
		if err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		if len(header) < 13 {
			break
		}

		lsn := binary.BigEndian.Uint64(header[0:8])
		if lsn <= fromLSN {
			payloadLen := binary.BigEndian.Uint32(header[9:13])
			skip := make([]byte, payloadLen)
			reader.Read(skip)
			continue
		}

		entryType := EntryType(header[8])
		payloadLen := binary.BigEndian.Uint32(header[9:13])

		payloadBytes := make([]byte, payloadLen)
		if _, err := reader.Read(payloadBytes); err != nil {
			logger.Warn("WAL replay: failed to read payload",
				logger.S("symbol", w.symbol),
				logger.I64("lsn", int64(lsn)),
				logger.Err(err),
			)
			break
		}

		var payload interface{}
		switch entryType {
		case EntryTypeCommand:
			var cmd CommandPayload
			if err := gob.NewDecoder(bytes.NewReader(payloadBytes)).Decode(&cmd); err != nil {
				logger.Warn("WAL replay: failed to decode command",
					logger.S("symbol", w.symbol),
					logger.I64("lsn", int64(lsn)),
					logger.Err(err),
				)
				continue
			}
			payload = cmd
		case EntryTypeCancel:
			var cancel CancelPayload
			if err := gob.NewDecoder(bytes.NewReader(payloadBytes)).Decode(&cancel); err != nil {
				logger.Warn("WAL replay: failed to decode cancel",
					logger.S("symbol", w.symbol),
					logger.I64("lsn", int64(lsn)),
					logger.Err(err),
				)
				continue
			}
			payload = cancel
		case EntryTypeTrade:
			var trade TradePayload
			if err := gob.NewDecoder(bytes.NewReader(payloadBytes)).Decode(&trade); err != nil {
				logger.Warn("WAL replay: failed to decode trade",
					logger.S("symbol", w.symbol),
					logger.I64("lsn", int64(lsn)),
					logger.Err(err),
				)
				continue
			}
			payload = trade
		}

		entry := &Entry{LSN: lsn, Type: entryType, Payload: payload}
		if err := handler(entry); err != nil {
			return fmt.Errorf("WAL replay handler error at LSN %d: %w", lsn, err)
		}
	}

	return nil
}

// Truncate removes all entries with LSN <= upToLSN from the WAL file.
func (w *WAL) Truncate(upToLSN uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncateLocked(upToLSN)
}

// Sync forces a flush of the WAL buffer to disk.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		return w.writer.Flush()
	}
	return nil
}

// SnapshotLSN atomically captures the current LSN for snapshotting.
// This ensures the snapshot and truncate operations use the same LSN.
func (w *WAL) SnapshotLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lsn.Load()
}

// WriteAtomicSnapshot writes snapshot data atomically and truncates WAL in one operation.
// This prevents race conditions where new entries are added between getting the LSN and truncating.
func (w *WAL) WriteAtomicSnapshot(data []byte, walLSN uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Write the snapshot data to a temporary file
	tmpPath := w.path + ".snap.tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp snapshot file: %w", err)
	}

	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write snapshot data: %w", err)
	}

	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close snapshot file: %w", err)
	}

	// Atomically rename the temp file to the final snapshot file
	snapPath := w.path + ".snap"
	if err := os.Rename(tmpPath, snapPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename snapshot file: %w", err)
	}

	// Now truncate the WAL up to the snapshot LSN
	// This is done atomically with the snapshot write under the same lock
	if err := w.truncateLocked(walLSN); err != nil {
		logger.Warn("snapshot saved but WAL truncation failed",
			logger.S("wal_path", w.path),
			logger.I64("wal_lsn", int64(walLSN)),
			logger.Err(err),
		)
		return err
	}

	logger.Info("WAL atomic snapshot completed",
		logger.S("symbol", w.symbol),
		logger.I64("wal_lsn", int64(walLSN)),
	)

	return nil
}

// truncateLocked truncates WAL entries up to upToLSN (must be called with w.mu held)
func (w *WAL) truncateLocked(upToLSN uint64) error {
	type record struct {
		lsn   uint64
		bytes []byte
	}
	var records []record

	file, err := os.Open(w.path)
	if err != nil {
		return fmt.Errorf("failed to open WAL for truncation: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	for {
		header := make([]byte, 13)
		_, err := reader.Read(header)
		if err != nil {
			break
		}
		if len(header) < 13 {
			break
		}

		lsn := binary.BigEndian.Uint64(header[0:8])
		payloadLen := binary.BigEndian.Uint32(header[9:13])
		totalLen := 13 + int(payloadLen)

		recordBytes := make([]byte, totalLen)
		copy(recordBytes[:13], header)

		if _, err := reader.Read(recordBytes[13:]); err != nil {
			break
		}

		if lsn > upToLSN {
			records = append(records, record{lsn: lsn, bytes: recordBytes})
		}
	}
	file.Close()

	newFile, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("failed to create new WAL file: %w", err)
	}

	writer := bufio.NewWriter(newFile)
	var newLastLSN uint64

	for _, rec := range records {
		if _, err := writer.Write(rec.bytes); err != nil {
			return fmt.Errorf("failed to write truncated WAL: %w", err)
		}
		newLastLSN = rec.lsn
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush truncated WAL: %w", err)
	}

	if err := newFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync truncated WAL: %w", err)
	}

	// Update WAL's internal file and writer references
	newFile.Close()
	w.file.Close()
	newFile, err = os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to reopen WAL after truncate: %w", err)
	}
	w.file = newFile
	w.writer = bufio.NewWriter(newFile)

	w.lsn.Store(newLastLSN)

	logger.Info("WAL truncated",
		logger.S("symbol", w.symbol),
		logger.I64("up_to_lsn", int64(upToLSN)),
		logger.I64("new_last_lsn", int64(newLastLSN)),
		logger.I("remaining_records", len(records)),
	)

	return nil
}
