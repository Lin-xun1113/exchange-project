package workflow_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/order/workflow"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func TestStaleAttemptCannotCompleteOrReschedule(t *testing.T) {
	for _, test := range []struct {
		name     string
		alter    string
		fail     bool
		attempts int
	}{
		{"stolen", "attempts = 2", false, 2},
		{"expired", "locked_until = TIMESTAMPADD(SECOND, -1, NOW(6))", false, 1},
		{"stolen failure", "attempts = 2", true, 2},
		{"expired failure", "locked_until = TIMESTAMPADD(SECOND, -1, NOW(6))", true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := mysqltest.Open(t)
			require.NoError(t, workflow.Enqueue(db, "order", "freeze", nil))
			claimed := make(chan workflow.Entry, 1)
			finish := make(chan struct{})
			defer close(finish)
			handler := func(_ context.Context, e workflow.Entry) (func(*gorm.DB) error, error) {
				claimed <- e
				<-finish
				if test.fail {
					return nil, status.Error(codes.Unavailable, "old attempt failed")
				}
				return func(tx *gorm.DB) error { return workflow.Enqueue(tx, e.AggregateID, "submit", nil) }, nil
			}
			w := workflow.NewWorker(db, map[string]workflow.Handler{"freeze": handler}, workflow.Options{Concurrency: 1}, nil)
			stop, done := startWorker(t, w)
			e := receive(t, claimed)
			require.Equal(t, 1, e.Attempts)
			require.NoError(t, db.Exec("UPDATE outbox SET "+test.alter+" WHERE id = ?", e.ID).Error)
			stop()
			finish <- struct{}{}
			require.NoError(t, receive(t, done))

			var rows []workflow.Entry
			require.NoError(t, db.Find(&rows).Error)
			require.Len(t, rows, 1, "the stale callback cannot enqueue a successor")
			require.Equal(t, "pending", rows[0].Status)
			require.Equal(t, test.attempts, rows[0].Attempts)
			require.Nil(t, rows[0].LastError)
			require.NotNil(t, rows[0].LockedUntil)

			// The next rightful attempt can commit the effect once.
			require.NoError(t, db.Exec("UPDATE outbox SET locked_until = TIMESTAMPADD(SECOND, -1, NOW(6)) WHERE id = ?", e.ID).Error)
			newAttempt := func(_ context.Context, e workflow.Entry) (func(*gorm.DB) error, error) {
				if e.Attempts != test.attempts+1 {
					return nil, fmt.Errorf("attempt = %d, expected %d", e.Attempts, test.attempts+1)
				}
				return func(tx *gorm.DB) error { return workflow.Enqueue(tx, e.AggregateID, "submit", nil) }, nil
			}
			startWorker(t, workflow.NewWorker(db, map[string]workflow.Handler{
				"freeze": newAttempt,
				"submit": func(context.Context, workflow.Entry) (func(*gorm.DB) error, error) { return nil, nil },
			}, workflow.Options{Concurrency: 1, Poll: 5 * time.Millisecond}, nil))
			awaitOutbox(t, db, "order", "submit", "done")
			e = awaitOutbox(t, db, "order", "freeze", "done")
			require.Equal(t, test.attempts+1, e.Attempts)
		})
	}
}

func TestLeaseExpiryDuringCommitRollsBackEffects(t *testing.T) {
	db := mysqltest.Open(t)
	require.NoError(t, workflow.Enqueue(db, "order", "freeze", nil))
	insideCommit := make(chan struct{}, 1)
	finish := make(chan struct{})
	defer close(finish)
	handler := func(context.Context, workflow.Entry) (func(*gorm.DB) error, error) {
		return func(tx *gorm.DB) error {
			if err := workflow.Enqueue(tx, "order", "submit", nil); err != nil {
				return err
			}
			insideCommit <- struct{}{}
			<-finish
			return nil
		}, nil
	}
	w := workflow.NewWorker(db, map[string]workflow.Handler{"freeze": handler}, workflow.Options{Concurrency: 1, Lease: 500 * time.Millisecond}, nil)
	stop, done := startWorker(t, w)
	receive(t, insideCommit)
	stop()
	require.Eventually(t, func() bool {
		var n int64
		return db.Model(&workflow.Entry{}).Where("step = 'freeze' AND locked_until <= NOW(6)").Count(&n).Error == nil && n == 1
	}, 2*time.Second, 5*time.Millisecond)
	finish <- struct{}{}
	require.NoError(t, receive(t, done))
	var rows []workflow.Entry
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status)
}

func TestFinalLeaseExpiryDoesNotDispatchAgain(t *testing.T) {
	db := mysqltest.Open(t)
	require.NoError(t, workflow.Enqueue(db, "order", "freeze", nil))
	var calls atomic.Int32
	claimed := make(chan workflow.Entry, 1)
	finish := make(chan struct{})
	defer close(finish)
	handler := func(_ context.Context, e workflow.Entry) (func(*gorm.DB) error, error) {
		if calls.Add(1) != 1 {
			return nil, status.Error(codes.Unavailable, "unexpected second invocation")
		}
		claimed <- e
		<-finish
		return func(tx *gorm.DB) error { return workflow.Enqueue(tx, e.AggregateID, "submit", nil) }, nil
	}
	w := workflow.NewWorker(db, map[string]workflow.Handler{"freeze": handler}, workflow.Options{Concurrency: 1, Poll: 5 * time.Millisecond, MaxAttempts: 1}, nil)
	stop, done := startWorker(t, w)
	e := receive(t, claimed)
	require.Equal(t, 1, e.Attempts)
	// Expire the last lease while its downstream call is still in flight.
	require.NoError(t, db.Exec("UPDATE outbox SET locked_until = TIMESTAMPADD(SECOND, -1, NOW(6)) WHERE id = ?", e.ID).Error)
	finish <- struct{}{}

	e = awaitOutbox(t, db, "order", "freeze", "dead")
	stop()
	require.NoError(t, receive(t, done))
	require.EqualValues(t, 1, calls.Load(), "the expired final attempt must not be dispatched again")
	require.Equal(t, 1, e.Attempts)
	require.Nil(t, e.LockedUntil)
	var rows int64
	require.NoError(t, db.Model(&workflow.Entry{}).Count(&rows).Error)
	require.EqualValues(t, 1, rows, "the expired callback must not enqueue a successor")
}
