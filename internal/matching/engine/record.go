package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
)

type submitRec struct {
	O                string    `json:"o"`
	U                int64     `json:"u"`
	S                book.Side `json:"s"`
	K                book.Kind `json:"k"`
	P                int64     `json:"p"`
	Q                int64     `json:"q"`
	T                int64     `json:"t"`
	CapacityRejected bool      `json:"capacity_rejected,omitempty"`
}

func (r submitRec) order() book.Order {
	return book.Order{ID: r.O, UserID: r.U, Side: r.S, Kind: r.K, Price: r.P, Qty: r.Q}
}

type cancelRec struct {
	O string `json:"o"`
	T int64  `json:"t"`
}

type orderRecord struct {
	Submit          *SubmitResult
	Cancelled       bool
	CancelRemaining int64
}

type records struct {
	byID  map[string]orderRecord
	final []string
	next  int
	limit int
}

func newRecords(limit int) records {
	return records{byID: make(map[string]orderRecord), limit: limit}
}

func (r *records) finalize(id string) {
	if len(r.final) < r.limit {
		r.final = append(r.final, id)
		return
	}
	delete(r.byID, r.final[r.next])
	r.final[r.next] = id
	r.next = (r.next + 1) % r.limit
}

func cloneSubmit(r *SubmitResult) *SubmitResult {
	cloned := *r
	cloned.Trades = slices.Clone(r.Trades)
	return &cloned
}

func (a *actor) apply(lsn uint64, rec any) response {
	a.lsn = lsn
	switch r := rec.(type) {
	case submitRec:
		return response{submit: a.applySubmit(lsn, r)}
	case cancelRec:
		remaining, found := a.book.Cancel(r.O)
		if !found {
			panic("persisted cancel is missing from book")
		}
		saved := a.records.byID[r.O]
		saved.Cancelled, saved.CancelRemaining = true, remaining
		a.records.byID[r.O] = saved
		a.records.finalize(r.O)
		return response{cancel: CancelResult{Found: true, Remaining: remaining}}
	default:
		panic("unknown matching record")
	}
}

func (a *actor) applySubmit(lsn uint64, rec submitRec) *SubmitResult {
	result := book.Result{Remaining: rec.Q}
	if !rec.CapacityRejected {
		result = a.book.Submit(rec.order())
	} else {
		// Preserve the existing business outcome for non-resting FOK and market orders.
		switch rec.K {
		case book.FOK:
			result.Reject = book.RejectFOKUnfilled
		case book.Market:
			result.Reject = book.RejectNoLiquidity
		}
	}
	r := &SubmitResult{
		OrderID: rec.O, Filled: result.Filled, Remaining: result.Remaining,
		Resting: result.Resting, Reject: result.Reject, LSN: lsn,
	}
	if len(result.Fills) > 0 {
		r.Trades = make([]Trade, len(result.Fills))
		at := time.Unix(0, rec.T).UTC()
		for i, f := range result.Fills {
			t := Trade{
				ID: fmt.Sprintf("%s-%d-%d", a.symbol, lsn, i), Price: f.Price, Qty: f.Qty,
				TakerSide: rec.S, LSN: lsn, At: at,
			}
			if rec.S == book.Buy {
				t.BuyOrderID, t.BuyUserID, t.SellOrderID, t.SellUserID = rec.O, rec.U, f.MakerID, f.MakerUserID
			} else {
				t.BuyOrderID, t.BuyUserID, t.SellOrderID, t.SellUserID = f.MakerID, f.MakerUserID, rec.O, rec.U
			}
			r.Trades[i] = t
			if !a.book.Has(f.MakerID) {
				a.records.finalize(f.MakerID)
			}
		}
	}
	a.records.byID[rec.O] = orderRecord{Submit: r}
	if !r.Resting {
		a.records.finalize(rec.O)
	}
	return r
}

func (a *actor) replay(r wal.Record) error {
	switch r.Type {
	case wal.RecordSubmit:
		var rec submitRec
		if err := json.Unmarshal(r.Payload, &rec); err != nil {
			return err
		}
		o := rec.order()
		if !validOrder(o) || a.book.Has(o.ID) {
			return fmt.Errorf("invalid or live duplicate submit %q", o.ID)
		}
		if (a.book.CheckSubmit(o) != nil) != rec.CapacityRejected {
			return fmt.Errorf("invalid capacity result for submit %q", o.ID)
		}
		// A cache size change can retain an older result whose ID was
		// legitimately reused after eviction in the original run.
		a.records.forgetFinal(o.ID)
		a.apply(r.LSN, rec)
	case wal.RecordCancel:
		var rec cancelRec
		if err := json.Unmarshal(r.Payload, &rec); err != nil {
			return err
		}
		if !validID(rec.O) || !a.book.Has(rec.O) {
			return fmt.Errorf("invalid cancel %q", rec.O)
		}
		a.apply(r.LSN, rec)
	default:
		return fmt.Errorf("unknown record type %d", r.Type)
	}
	return nil
}

// forgetFinal only scans the ring when replay encounters a retained ID that
// was already evicted with the original cache size.
func (r *records) forgetFinal(id string) {
	if _, exists := r.byID[id]; !exists {
		return
	}
	delete(r.byID, id)
	if r.next != 0 {
		ordered := make([]string, 0, len(r.final))
		ordered = append(ordered, r.final[r.next:]...)
		r.final = append(ordered, r.final[:r.next]...)
		r.next = 0
	}
	if i := slices.Index(r.final, id); i >= 0 {
		r.final = slices.Delete(r.final, i, i+1)
	}
}

type snapshotBody struct {
	Symbol    string                 `json:"symbol"`
	LSN       uint64                 `json:"lsn"`
	Orders    []book.Resting         `json:"orders"`
	Records   map[string]orderRecord `json:"records"`
	FinalRing []string               `json:"final_ring"`
}

func (a *actor) capture() snapshotBody {
	final := make([]string, 0, len(a.records.final))
	final = append(final, a.records.final[a.records.next:]...)
	final = append(final, a.records.final[:a.records.next]...)
	state := snapshotBody{
		Symbol: a.symbol, LSN: a.lsn, Orders: a.book.Resting(), FinalRing: final,
		Records: make(map[string]orderRecord, len(a.records.byID)),
	}
	// Submit values are immutable and API responses always own their slices.
	for id, rec := range a.records.byID {
		state.Records[id] = rec
	}
	return state
}

func (a *actor) restore(lsn uint64, body []byte) error {
	var s snapshotBody
	if err := json.Unmarshal(body, &s); err != nil {
		return fmt.Errorf("decode snapshot %d: %w", lsn, err)
	}
	if s.LSN != lsn || s.Symbol != a.symbol {
		return fmt.Errorf("snapshot identity mismatch: symbol %q LSN %d", s.Symbol, s.LSN)
	}
	if err := a.book.Restore(s.Orders); err != nil {
		return err
	}
	for _, o := range s.Orders {
		r, ok := s.Records[o.ID]
		if !ok || r.Submit == nil || r.Cancelled || !r.Submit.Resting || r.Submit.Remaining < o.Remaining {
			return fmt.Errorf("invalid live snapshot record %q", o.ID)
		}
	}
	if len(s.Records)-a.book.Len() != len(s.FinalRing) {
		return fmt.Errorf("snapshot records and final ring disagree")
	}
	for id, record := range s.Records {
		r := record.Submit
		if !validID(id) || r == nil || r.OrderID != id || r.LSN == 0 || r.LSN > lsn || r.Filled < 0 || r.Remaining < 0 ||
			(record.Cancelled && (!r.Resting || record.CancelRemaining <= 0 || record.CancelRemaining > r.Remaining)) {
			return fmt.Errorf("invalid snapshot result %q", id)
		}
	}
	// Remove final records from the lookup as we check the ring, so duplicate
	// IDs cannot mask a missing record. Reinsert the newest retained entries.
	final := s.FinalRing
	keepFrom := max(0, len(final)-a.records.limit)
	kept := make([]orderRecord, 0, len(final)-keepFrom)
	for i, id := range final {
		r, ok := s.Records[id]
		if !ok || a.book.Has(id) {
			return fmt.Errorf("invalid final snapshot record %q", id)
		}
		if i >= keepFrom {
			kept = append(kept, r)
		}
		delete(s.Records, id)
	}
	if s.Records != nil {
		a.records.byID = s.Records
	}
	a.records.final = final[keepFrom:]
	for i, id := range a.records.final {
		a.records.byID[id] = kept[i]
	}
	a.lsn = lsn
	return nil
}
