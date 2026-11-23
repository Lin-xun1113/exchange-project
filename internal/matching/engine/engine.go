// Package engine owns one matching actor and durable command stream per market.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/snapshot"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/symbol"
)

type Config struct {
	WALDir, SnapshotDir string
	Sync                wal.SyncMode
	SnapshotEvery       uint64
	SnapshotInterval    time.Duration
	MailboxSize         int
	ResultCache         int
	Symbols             []string
	Metrics             *metrics.Matching
}

type Trade struct {
	ID, BuyOrderID, SellOrderID string
	BuyUserID, SellUserID       int64
	Price, Qty                  int64
	TakerSide                   book.Side
	LSN                         uint64
	At                          time.Time
}

type SubmitResult struct {
	OrderID           string
	Trades            []Trade
	Filled, Remaining int64
	Resting           bool
	Reject            book.Reject
	LSN               uint64
}

type CancelResult struct {
	Found     bool
	Remaining int64
}

type Depth struct {
	Bids, Asks []book.Level
	LSN        uint64
}

var (
	ErrUnknownSymbol = errors.New("unknown symbol")
	ErrClosed        = errors.New("matcher closed")
	ErrInvalidOrder  = errors.New("invalid matching order")
)

type Matcher struct {
	actors   map[string]*actor // immutable after recovery
	sendMu   sync.RWMutex      // readers own permission to send before the mailboxes are closed
	stopOnce sync.Once
	stopping chan struct{}
	done     chan struct{}
	closeErr error // read only after done closes
}

// New recovers every configured market before any actor starts serving.
func New(cfg Config) (*Matcher, error) {
	m, err := recoverMatcher(cfg)
	if err != nil {
		return nil, err
	}
	m.start()
	return m, nil
}

func recoverMatcher(cfg Config) (*Matcher, error) {
	if cfg.WALDir == "" || cfg.SnapshotDir == "" || len(cfg.Symbols) == 0 {
		return nil, errors.New("matching requires WAL and snapshot directories and at least one symbol")
	}
	if cfg.Sync == 0 {
		cfg.Sync = wal.SyncFsync
	}
	if cfg.SnapshotEvery == 0 {
		cfg.SnapshotEvery = 50_000
	}
	if cfg.SnapshotInterval <= 0 {
		cfg.SnapshotInterval = time.Minute
	}
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 4096
	}
	if cfg.ResultCache <= 0 {
		cfg.ResultCache = 100_000
	}

	pairs := make([]string, 0, len(cfg.Symbols))
	allowed := make(map[string]struct{}, len(cfg.Symbols))
	for _, raw := range cfg.Symbols {
		p, err := symbol.Parse(raw)
		if err != nil {
			return nil, err
		}
		pair := p.String()
		dir := wal.SanitizeSymbol(pair)
		if _, duplicate := allowed[dir]; duplicate {
			return nil, fmt.Errorf("duplicate symbol %q", pair)
		}
		allowed[dir] = struct{}{}
		pairs = append(pairs, pair)
	}
	if err := checkWALDirs(cfg.WALDir, allowed); err != nil {
		return nil, err
	}

	m := &Matcher{
		actors:   make(map[string]*actor, len(pairs)),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, pair := range pairs {
		a, err := recoverActor(cfg, pair)
		if err != nil {
			for _, opened := range m.actors {
				err = errors.Join(err, opened.wal.Close())
			}
			return nil, fmt.Errorf("recover %s: %w", pair, err)
		}
		m.actors[pair] = a
	}
	return m, nil
}

func checkWALDirs(dir string, allowed map[string]struct{}) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, ok := allowed[e.Name()]; !ok && e.IsDir() {
			return fmt.Errorf("wal data for unconfigured symbol %q", e.Name())
		}
	}
	return nil
}

func symbolDir(root, pair string) string {
	return filepath.Join(root, wal.SanitizeSymbol(pair))
}

func recoverActor(cfg Config, pair string) (*actor, error) {
	a := &actor{
		symbol:    pair,
		mailbox:   make(chan command, cfg.MailboxSize),
		done:      make(chan struct{}),
		book:      book.New(),
		records:   newRecords(cfg.ResultCache),
		metrics:   cfg.Metrics,
		snapDir:   symbolDir(cfg.SnapshotDir, pair),
		interval:  cfg.SnapshotInterval,
		threshold: cfg.SnapshotEvery,
	}
	lsn, body, err := snapshot.Latest(a.snapDir)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if err := a.restore(lsn, body); err != nil {
			return nil, err
		}
		a.snapLSN, a.snapAttempt, a.hasSnapshot = lsn, lsn, true
	}
	a.wal, err = wal.Open(symbolDir(cfg.WALDir, pair), cfg.Sync)
	if err != nil {
		return nil, err
	}
	if err := a.wal.Replay(lsn, a.replay); err != nil {
		return nil, errors.Join(err, a.wal.Close())
	}
	return a, nil
}

func (m *Matcher) start() {
	for _, a := range m.actors {
		go a.run()
	}
}

func (m *Matcher) Submit(ctx context.Context, pair string, o book.Order) (*SubmitResult, error) {
	if !validOrder(o) {
		return nil, ErrInvalidOrder
	}
	r, err := m.dispatch(ctx, pair, command{op: opSubmit, order: o})
	return r.submit, err
}

func (m *Matcher) Cancel(ctx context.Context, pair, id string) (CancelResult, error) {
	if !validID(id) {
		return CancelResult{}, ErrInvalidOrder
	}
	r, err := m.dispatch(ctx, pair, command{op: opCancel, order: book.Order{ID: id}})
	return r.cancel, err
}

func (m *Matcher) Depth(ctx context.Context, pair string, n int) (Depth, error) {
	if n < 1 || n > 200 {
		return Depth{}, errors.New("depth must be between 1 and 200")
	}
	r, err := m.dispatch(ctx, pair, command{op: opDepth, depth: n})
	return r.depth, err
}

func (m *Matcher) Snapshot(ctx context.Context, pair string) error {
	_, err := m.dispatch(ctx, pair, command{op: opSnapshot})
	return err
}

func validID(id string) bool {
	return len(id) > 0 && len(id) <= 64*utf8.UTFMax && utf8.ValidString(id) && utf8.RuneCountInString(id) <= 64
}

func validOrder(o book.Order) bool {
	if !validID(o.ID) || o.UserID <= 0 || o.Qty <= 0 || (o.Side != book.Buy && o.Side != book.Sell) {
		return false
	}
	switch o.Kind {
	case book.Limit, book.FOK, book.IOC:
		return o.Price > 0
	case book.Market:
		return o.Side == book.Sell && o.Price == 0 // Market buys lack a frozen quote budget.
	default:
		return false
	}
}

func (m *Matcher) dispatch(ctx context.Context, pair string, cmd command) (response, error) {
	a, ok := m.actors[pair]
	if !ok {
		return response{}, ErrUnknownSymbol
	}
	cmd.reply = make(chan response, 1)
	if err := m.enqueue(ctx, a, cmd); err != nil {
		return response{}, err
	}
	select {
	case r := <-cmd.reply:
		return r, r.err
	case <-ctx.Done():
		return response{}, ctx.Err()
	}
}

func (m *Matcher) enqueue(ctx context.Context, a *actor, cmd command) error {
	m.sendMu.RLock()
	defer m.sendMu.RUnlock()
	select {
	case <-m.stopping:
		return ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.mailbox <- cmd:
		return nil
	case <-m.stopping:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close rejects new sends and drains accepted commands. The ongoing close
// remains in effect even if this caller's context expires.
func (m *Matcher) Close(ctx context.Context) error {
	m.stopOnce.Do(func() {
		close(m.stopping) // Unblock enqueuers before waiting on their read locks.
		go m.shutdown()
	})
	select {
	case <-m.done:
		return m.closeErr
	default:
	}
	select {
	case <-m.done:
		return m.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Matcher) shutdown() {
	m.sendMu.Lock()
	for _, a := range m.actors {
		close(a.mailbox)
	}
	m.sendMu.Unlock()
	for _, a := range m.actors {
		<-a.done
		if a.closeErr != nil {
			m.closeErr = errors.Join(m.closeErr, fmt.Errorf("close %s: %w", a.symbol, a.closeErr))
		}
	}
	close(m.done)
}
