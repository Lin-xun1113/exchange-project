package engine

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/snapshot"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"go.uber.org/zap"
)

const maxBatch = 256

type operation uint8

const (
	opSubmit operation = iota
	opCancel
	opDepth
	opSnapshot
)

type command struct {
	op    operation
	order book.Order
	depth int
	reply chan response
}

type response struct {
	submit *SubmitResult
	cancel CancelResult
	depth  Depth
	err    error
}

type actor struct {
	symbol   string
	mailbox  chan command
	done     chan struct{}
	closeErr error // read only after done closes

	book    *book.Book
	wal     *wal.Log
	records records
	lsn     uint64
	metrics *metrics.Matching

	snapDir     string
	interval    time.Duration
	threshold   uint64
	snapLSN     uint64
	snapAttempt uint64
	hasSnapshot bool
	writing     <-chan snapshotDone
	writingLSN  uint64
	waiters     []chan response
	pending     []chan response
}

func (a *actor) run() {
	defer close(a.done)
	defer func() {
		if v := recover(); v != nil {
			logger.Fatal("matching actor panic", zap.String("symbol", a.symbol), zap.Any("panic", v), zap.ByteString("stack", debug.Stack()))
		}
	}()
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	a.observeResting()

	var buffer [maxBatch]command
	for {
		select {
		case cmd, ok := <-a.mailbox:
			if !ok {
				a.closeErr = a.finish()
				return
			}
			batch := a.batch(buffer[:0], cmd)
			a.process(batch)
			clear(batch)
		case done := <-a.writing:
			a.finishSnapshot(done)
		case <-ticker.C:
			if a.lsn > a.snapLSN {
				a.startSnapshot(nil)
			}
		}
		// A batch can cross the threshold while a snapshot is writing. Recheck
		// on completion so it does not have to wait for another command.
		if a.writing == nil && a.lsn-a.snapAttempt >= a.threshold {
			a.startSnapshot(nil)
		}
	}
}

func (a *actor) batch(batch []command, first command) []command {
	batch = append(batch, first)
	// A manual snapshot is a barrier. Capture it after syncing this batch.
	for len(batch) < cap(batch) && batch[len(batch)-1].op != opSnapshot {
		select {
		case cmd, ok := <-a.mailbox:
			if !ok {
				return batch
			}
			batch = append(batch, cmd)
		default:
			return batch
		}
	}
	return batch
}

func (a *actor) process(batch []command) {
	var replies [maxBatch]response
	before := a.lsn
	for i, cmd := range batch {
		r, err := a.execute(cmd)
		if err != nil {
			a.failStop(batch, "wal append failed", err)
			return
		}
		replies[i] = r
	}
	if a.lsn != before {
		if err := a.syncWAL(); err != nil {
			a.failStop(batch, "wal sync failed", err)
			return
		}
		a.observeResting()
	}
	if a.metrics != nil {
		a.metrics.BatchSize(len(batch))
	}
	for i, cmd := range batch {
		if cmd.op == opSnapshot {
			a.startSnapshot(cmd.reply)
		} else {
			cmd.reply <- replies[i]
		}
	}
}

func (a *actor) execute(cmd command) (response, error) {
	if cmd.op == opSnapshot {
		return response{}, nil
	}
	if a.metrics != nil {
		start := time.Now()
		defer func() {
			a.metrics.Command(a.symbol, [...]string{"submit", "cancel", "depth"}[cmd.op], time.Since(start))
		}()
	}
	switch cmd.op {
	case opSubmit:
		o := cmd.order
		if saved, ok := a.records.byID[o.ID]; ok {
			return response{submit: cloneSubmit(saved.Submit)}, nil
		}
		// A level overflow or oversized fill list produces a durable terminal
		// result. The full remainder lets order-svc release the freeze.
		capacityRejected := a.book.CheckSubmit(o) != nil
		rec := submitRec{O: o.ID, U: o.UserID, S: o.Side, K: o.Kind, P: o.Price, Q: o.Qty, T: time.Now().UnixNano(), CapacityRejected: capacityRejected}
		lsn, err := a.append(wal.RecordSubmit, rec)
		if err != nil {
			return response{}, err
		}
		r := a.apply(lsn, rec)
		if a.metrics != nil && len(r.submit.Trades) > 0 {
			a.metrics.Trades(a.symbol, len(r.submit.Trades))
		}
		r.submit = cloneSubmit(r.submit)
		return r, nil
	case opCancel:
		id := cmd.order.ID
		if saved, ok := a.records.byID[id]; ok && saved.Cancelled {
			return response{cancel: CancelResult{Found: true, Remaining: saved.CancelRemaining}}, nil
		}
		if !a.book.Has(id) {
			return response{}, nil
		}
		rec := cancelRec{O: id, T: time.Now().UnixNano()}
		lsn, err := a.append(wal.RecordCancel, rec)
		if err != nil {
			return response{}, err
		}
		return a.apply(lsn, rec), nil
	case opDepth:
		bids, asks := a.book.Depth(cmd.depth)
		return response{depth: Depth{Bids: bids, Asks: asks, LSN: a.lsn}}, nil
	default:
		panic(fmt.Sprintf("unexpected actor operation %d", cmd.op))
	}
}

func (a *actor) append(t wal.RecordType, rec any) (uint64, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return 0, err
	}
	return a.wal.Append(t, data)
}

func (a *actor) syncWAL() error {
	if a.metrics == nil {
		return a.wal.Sync()
	}
	start := time.Now()
	err := a.wal.Sync()
	a.metrics.WALSync(time.Since(start))
	return err
}

func (a *actor) observeResting() {
	if a.metrics != nil {
		a.metrics.RestingOrders(a.symbol).Set(float64(a.book.Len()))
	}
}

func (a *actor) failStop(batch []command, msg string, err error) {
	for _, cmd := range batch {
		cmd.reply <- response{err: ErrClosed}
	}
	logger.Fatal(msg, zap.String("symbol", a.symbol), zap.Error(err))
}

type snapshotDone struct {
	lsn uint64
	err error
}

func (a *actor) startSnapshot(reply chan response) {
	if a.writing != nil {
		if reply != nil {
			if a.writingLSN == a.lsn {
				a.waiters = append(a.waiters, reply)
			} else {
				a.pending = append(a.pending, reply)
			}
		}
		return
	}
	if a.hasSnapshot && a.snapLSN == a.lsn {
		if reply != nil {
			reply <- response{}
		}
		return
	}
	if reply != nil {
		a.waiters = append(a.waiters, reply)
	}
	state := a.capture()
	done := make(chan snapshotDone, 1)
	a.writing, a.writingLSN, a.snapAttempt = done, state.LSN, state.LSN
	// The file writer never touches the actor's WAL or mailbox. This buffer
	// lets it report completion during shutdown without competing with senders.
	go func() { done <- snapshotDone{lsn: state.LSN, err: a.writeSnapshot(state)} }()
}

func (a *actor) finishSnapshot(done snapshotDone) {
	if done.err == nil {
		a.snapLSN, a.hasSnapshot = done.lsn, true
		a.compact()
	}
	for _, reply := range a.waiters {
		reply <- response{err: done.err}
	}
	clear(a.waiters)
	a.waiters = a.waiters[:0]
	a.writing = nil
	if len(a.pending) > 0 {
		a.waiters, a.pending = a.pending, a.waiters
		a.startSnapshot(nil)
	}
}

func (a *actor) writeSnapshot(state snapshotBody) error {
	start := time.Now()
	body, err := json.Marshal(state)
	if err == nil {
		err = snapshot.Write(a.snapDir, state.LSN, body)
	}
	result := "ok"
	if err != nil {
		result = "error"
		logger.Warn("snapshot write failed", zap.String("symbol", a.symbol), zap.Uint64("lsn", state.LSN), zap.Error(err))
	}
	if a.metrics != nil {
		a.metrics.Snapshot(a.symbol, result, time.Since(start))
	}
	return err
}

func (a *actor) compact() {
	oldest, err := snapshot.Prune(a.snapDir, 2)
	if err == nil && oldest != 0 {
		err = a.wal.Compact(oldest)
	}
	if err != nil {
		logger.Warn("snapshot compaction failed", zap.String("symbol", a.symbol), zap.Error(err))
	}
}

func (a *actor) finish() error {
	for a.writing != nil {
		a.finishSnapshot(<-a.writing)
	}
	var snapErr error
	if a.lsn > a.snapLSN {
		snapErr = a.writeSnapshot(a.capture())
		if snapErr == nil {
			a.compact()
		}
	}
	if err := a.wal.Close(); err != nil {
		a.failStop(nil, "wal close failed", err)
		return err
	}
	return snapErr
}
