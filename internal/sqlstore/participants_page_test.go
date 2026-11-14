package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// participantScanQuery counts grant-holder rows read and records their plans.
type participantScanQuery struct {
	Querier
	rows  int
	plans []string
}

func (q *participantScanQuery) Query(query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "SELECT DISTINCT session_id FROM grants") {
		for _, prefix := range []string{"EXPLAIN QUERY PLAN ", ""} {
			rows, err := q.Querier.Query(prefix+query, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				if prefix == "" {
					q.rows++
					continue
				}
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					return nil, err
				}
				q.plans = append(q.plans, detail)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	return q.Querier.Query(query, args...)
}

func TestParticipantsPageReadsOnlyNeededGrantHolders(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	const holders = 40
	for i := 0; i < holders; i++ {
		c, _ := delegate(t, f, f.admin.Token, fmt.Sprint("p", i), f.context.ID, model.PermissionRead, time.Hour)
		if i%3 == 0 {
			if err := f.svc.RevokeSession(ctx, f.admin.Token, c.Session.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	var all []model.Participant
	var candidates []string
	if err := f.store.View(ctx, func(r model.ReadStore) error {
		var err error
		if candidates, err = ParticipantCandidates(r.(*TxStore), f.context.ID, "", -1); err != nil {
			return err
		}
		all, err = r.(*TxStore).ParticipantsAfter(f.context.ID, *f.now, "", 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// through counts candidate holders after cursor up to and including last.
	through := func(cursor, last string) int {
		n := 0
		for _, id := range candidates {
			if id > cursor && (last == "" || id <= last) {
				n++
			}
		}
		return n
	}
	if want := holders - (holders+2)/3 + 1; len(all) != want {
		t.Fatalf("participants=%d want %d", len(all), want)
	}
	for _, limit := range []int{1, 3, 7} {
		var got []model.Participant
		after := ""
		for {
			var page []model.Participant
			var q *participantScanQuery
			if err := f.store.View(ctx, func(r model.ReadStore) error {
				q = &participantScanQuery{Querier: TxQuerier(r.(*TxStore))}
				var err error
				page, err = NewTxStore(q).ParticipantsAfter(f.context.ID, *f.now, after, limit)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			// Full pages read only the chunks up to their last row; a short
			// page has read every remaining holder.
			want := through(after, "")
			if len(page) == limit {
				want = min(want, (through(after, page[len(page)-1].SessionID)+limit-1)/limit*limit)
			}
			if q.rows != want {
				t.Fatalf("limit %d page after %q read %d grant holders, want %d", limit, after, q.rows, want)
			}
			plan := strings.Join(q.plans, "\n")
			if !strings.Contains(plan, "grants_context_sessions") || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatalf("participant seek not ordered by index:\n%s", plan)
			}
			got = append(got, page...)
			if len(page) < limit {
				break
			}
			after = page[len(page)-1].SessionID
		}
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("limit %d pages differ from full listing", limit)
		}
	}
}
