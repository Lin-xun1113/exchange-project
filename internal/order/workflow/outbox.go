// Package workflow runs the durable order workflow.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	FreezeStep  = "freeze"
	SubmitStep  = "submit"
	CancelStep  = "cancel"
	ReleaseStep = "release"
	settleStep  = "settle"

	due         = "status = 'pending' AND next_run_at <= NOW(6) AND (locked_until IS NULL OR locked_until <= NOW(6))"
	leaseHeld   = "id = ? AND status = 'pending' AND attempts = ? AND locked_until > NOW(6)"
	rpcTimeout  = 3 * time.Second
	dbTimeout   = 5 * time.Second
	dueInterval = 10 * time.Second
)

var (
	errLeaseLost         = errors.New("outbox lease lost")
	errAttemptsExhausted = errors.New("outbox attempt limit reached with no active lease")
)

type Entry struct {
	ID          int64
	AggregateID string
	Step        string
	Payload     []byte
	Status      string
	Attempts    int
	NextRunAt   time.Time
	LockedUntil *time.Time
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Entry) TableName() string { return "outbox" }

// Enqueue adds an immutable step within the caller's transaction.
func Enqueue(tx *gorm.DB, aggregateID, step string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Model(&Entry{}).Create(map[string]any{
		"aggregate_id": aggregateID,
		"step":         step,
		"payload":      string(body),
		"next_run_at":  gorm.Expr("NOW(6)"),
	}).Error
}

// Handler makes a time-bounded RPC and returns the corresponding local writes.
// The commit callback can be retried after a deadlock and must only use tx.
type Handler func(ctx context.Context, e Entry) (commit func(tx *gorm.DB) error, err error)

type Options struct {
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	MaxAttempts int
}

type Worker struct {
	db       *gorm.DB
	handlers map[string]Handler
	opt      Options
	metrics  *metrics.Order
}

func NewWorker(db *gorm.DB, handlers map[string]Handler, opt Options, m *metrics.Order) *Worker {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	if opt.Lease <= 0 {
		opt.Lease = 30 * time.Second
	}
	if opt.Poll <= 0 {
		opt.Poll = 200 * time.Millisecond
	}
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = 10
	}
	return &Worker{db: db, handlers: handlers, opt: opt, metrics: m}
}

// Run polls for due entries and drains claimed work on cancellation.
func (w *Worker) Run(ctx context.Context) error {
	poll := time.NewTicker(w.opt.Poll)
	defer poll.Stop()
	stats := time.NewTicker(dueInterval)
	defer stats.Stop()
	completed := make(chan struct{}, w.opt.Concurrency)
	workerCtx := context.WithoutCancel(ctx)
	active := 0
	for ctx.Err() == nil {
		if slots := w.opt.Concurrency - active; slots > 0 {
			entries, err := w.claim(ctx, slots)
			if err != nil && ctx.Err() == nil {
				logger.WithContext(ctx).Warn("claim outbox entries", logger.Err(err))
			}
			for _, e := range entries {
				active++
				go func() {
					defer func() { completed <- struct{}{} }()
					w.process(workerCtx, e)
				}()
			}
		}
		select {
		case <-ctx.Done():
		case <-poll.C:
		case <-completed:
			active--
		case <-stats.C:
			w.observeDue(ctx)
		}
	}
	for ; active > 0; active-- {
		<-completed
	}
	return nil
}

func (w *Worker) claim(ctx context.Context, limit int) ([]Entry, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	var entries []Entry
	type candidate struct {
		ID       int64
		Step     string
		Attempts int
	}
	var exhausted []candidate
	// READ COMMITTED avoids gap locks around due ranges that can block enqueues.
	err := repository.Transaction(w.db.WithContext(ctx), func(tx *gorm.DB) error {
		entries = nil
		exhausted = exhausted[:0]
		var candidates []candidate
		if err := tx.Model(&Entry{}).Select("id", "step", "attempts").Where(due).Order("next_run_at, id").Limit(limit).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&candidates).Error; err != nil || len(candidates) == 0 {
			return err
		}
		// The selected rows stay locked until commit. Do not renew or bump the
		// fencing token of an entry that has spent its final attempt.
		ids := make([]int64, 0, len(candidates))
		var exhaustedIDs []int64
		for _, c := range candidates {
			if c.Attempts >= w.opt.MaxAttempts {
				exhausted = append(exhausted, c)
				exhaustedIDs = append(exhaustedIDs, c.ID)
			} else {
				ids = append(ids, c.ID)
			}
		}
		if len(exhaustedIDs) > 0 {
			if err := tx.Model(&Entry{}).Where("id IN ?", exhaustedIDs).Updates(map[string]any{
				"status":       "dead",
				"locked_until": nil,
				"last_error":   errAttemptsExhausted.Error(),
			}).Error; err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Model(&Entry{}).Where("id IN ?", ids).UpdateColumns(map[string]any{
			"locked_until": gorm.Expr("TIMESTAMPADD(MICROSECOND, ?, NOW(6))", max(1, w.opt.Lease.Microseconds())),
			"attempts":     gorm.Expr("attempts + 1"),
		}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Find(&entries).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	for _, c := range exhausted {
		step, _, _ := strings.Cut(c.Step, ":")
		w.observeStep(step, "dead", start)
		logDead(ctx, c.ID, c.Step, c.Attempts, errAttemptsExhausted)
	}
	return entries, nil
}

func (w *Worker) process(ctx context.Context, e Entry) {
	start := time.Now()
	step, _, _ := strings.Cut(e.Step, ":")
	var commit func(*gorm.DB) error
	var err error
	if handler := w.handlers[step]; handler != nil {
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		commit, err = handler(rpcCtx, e)
		cancel()
	} else {
		err = status.Errorf(codes.FailedPrecondition, "unregistered outbox step %q", e.Step)
	}
	if err == nil {
		err = w.complete(ctx, e, commit)
		if err == nil {
			w.observeStep(step, "ok", start)
			return
		}
	}
	if errors.Is(err, errLeaseLost) {
		return
	}
	result := "retry"
	if e.Attempts >= w.opt.MaxAttempts || !retryable(err) {
		result = "dead"
	}
	if persistErr := w.fail(ctx, e, err, result == "dead"); persistErr != nil {
		if !errors.Is(persistErr, errLeaseLost) {
			logger.WithContext(ctx).Error("record outbox failure", logger.I64("id", e.ID), logger.Err(persistErr))
		}
		return
	}
	w.observeStep(step, result, start)
	if result == "dead" {
		logDead(ctx, e.ID, e.Step, e.Attempts, err)
	}
}

func logDead(ctx context.Context, id int64, step string, attempts int, err error) {
	logger.WithContext(ctx).Error("outbox step exhausted", logger.I64("id", id), logger.S("step", step), logger.I("attempts", attempts), logger.Err(err))
}

type finalStatusesKey struct{}

func (w *Worker) complete(ctx context.Context, e Entry, commit func(*gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(ctx, w.opt.Lease)
	defer cancel()
	var finals []model.OrderStatus
	if w.metrics != nil {
		ctx = context.WithValue(ctx, finalStatusesKey{}, &finals)
	}
	err := repository.Transaction(w.db.WithContext(ctx), func(tx *gorm.DB) error {
		finals = finals[:0]
		if err := lockLease(tx, e); err != nil {
			return err
		}
		if commit != nil {
			if err := commit(tx); err != nil {
				return err
			}
		}
		// Callbacks can wait on order rows. Recheck against the DB clock at
		// the end so expiry during that wait rolls back the callback as well.
		return updateLease(tx, e, map[string]any{"status": "done", "locked_until": nil, "last_error": nil})
	})
	if err == nil && w.metrics != nil {
		for _, final := range finals {
			w.metrics.OrderFinal(string(final))
		}
	}
	return err
}

func (w *Worker) fail(ctx context.Context, e Entry, cause error, dead bool) error {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	message := cause.Error()
	message = strings.ToValidUTF8(message[:min(len(message), 4096)], "\uFFFD")
	updates := map[string]any{"locked_until": nil, "last_error": message}
	if dead {
		updates["status"] = "dead"
	} else {
		delay := min(200*time.Millisecond<<min(e.Attempts-1, 8), 30*time.Second)
		delay = time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
		updates["next_run_at"] = gorm.Expr("TIMESTAMPADD(MICROSECOND, ?, NOW(6))", delay.Microseconds())
	}
	return repository.Transaction(w.db.WithContext(ctx), func(tx *gorm.DB) error {
		if err := lockLease(tx, e); err != nil {
			return err
		}
		return updateLease(tx, e, updates)
	})
}

func lockLease(tx *gorm.DB, e Entry) error {
	var row Entry
	res := tx.Select("id").Where(leaseHeld, e.ID, e.Attempts).
		Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find(&row)
	return leaseResult(res)
}

func updateLease(tx *gorm.DB, e Entry, updates map[string]any) error {
	return leaseResult(tx.Model(&Entry{}).Where(leaseHeld, e.ID, e.Attempts).Updates(updates))
}

func leaseResult(res *gorm.DB) error {
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errLeaseLost
	}
	return nil
}

func retryable(err error) bool {
	s, ok := status.FromError(err)
	if !ok {
		return true
	}
	switch s.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Aborted, codes.ResourceExhausted, codes.Internal, codes.Unknown, codes.Canceled:
		return true
	default:
		return false
	}
}

func (w *Worker) observeStep(step, result string, start time.Time) {
	if w.metrics != nil {
		// Only registered prefixes can become label values.
		if w.handlers[step] == nil {
			step = "unknown"
		}
		w.metrics.OutboxStep(step, result, time.Since(start))
	}
}

func (w *Worker) observeDue(ctx context.Context) {
	if w.metrics == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	var count int64
	if err := w.db.WithContext(ctx).Model(&Entry{}).Where(due).Count(&count).Error; err != nil {
		logger.WithContext(ctx).Warn("count due outbox entries", logger.Err(err))
		return
	}
	w.metrics.OutboxDue().Set(float64(count))
}
