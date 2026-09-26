// outbox_db_test.go — the sync outbox against a real database: the
// properties the client's contiguous cursor rests on, under concurrent
// writers and rollbacks. Gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand/v2"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/migrate"
)

// TestOutboxGapFreeUnderConcurrentWrites: writers commit and roll back
// multi-record transactions concurrently while a reader follows the delta
// the way a client does. Rolled-back claims leave no hole, every snapshot
// the reader takes is a contiguous tail past its cursor (claims commit in
// order), and the reader ends with exactly the committed records it may
// see, each once.
func TestOutboxGapFreeUnderConcurrentWrites(t *testing.T) {
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	wsID := testUUID()
	if _, err := pool.Exec(ctx,
		`insert into workspaces (id, name, slug) values ($1, 'outbox soak', $2)`,
		wsID, "outbox-soak-"+wsID[:8]); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() {
		// Cascades to the outbox and the sequence row.
		if _, err := pool.Exec(context.Background(), `delete from workspaces where id = $1`, wsID); err != nil {
			t.Logf("cleanup workspace %s: %v", wsID, err)
		}
	})

	a := &API{pool: pool}
	reader, other := testUUID(), testUUID()

	var (
		mu         sync.Mutex
		committed  int
		rolledBack int
		visible    []int64 // committed sequences the reader may see
	)
	const writers, txPerWriter = 12, 30
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			rnd := mrand.New(mrand.NewPCG(uint64(w), 7))
			for range txPerWriter {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Errorf("begin: %v", err)
					return
				}
				var recs []syncActionRecord
				for range 1 + rnd.IntN(3) {
					model, data := "Issue", map[string]any{"title": "soak"}
					if rnd.IntN(3) == 0 {
						recipient := other
						if rnd.IntN(2) == 0 {
							recipient = reader
						}
						model, data = notifModel, map[string]any{"recipientId": recipient}
					}
					rec, err := a.emitChange(ctx, tx, wsID, model, testUUID(), "UPDATE", data)
					if err != nil {
						_ = tx.Rollback(ctx)
						t.Errorf("emitChange: %v", err)
						return
					}
					recs = append(recs, rec)
				}
				if rnd.IntN(10) < 3 {
					if err := tx.Rollback(ctx); err != nil {
						t.Errorf("rollback: %v", err)
						return
					}
					mu.Lock()
					rolledBack += len(recs)
					mu.Unlock()
					continue
				}
				if err := tx.Commit(ctx); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				mu.Lock()
				for _, rec := range recs {
					committed++
					if rec.ModelName != notifModel || rec.recipient == reader {
						seq, _ := strconv.ParseInt(rec.SequenceID, 10, 64) // emitChange formats it
						visible = append(visible, seq)
					}
				}
				mu.Unlock()
			}
		})
	}

	// The reader: the delta handler's reads (head, then the outbox scan)
	// and the client's max-only cursor.
	var (
		cursor int64
		got    []int64
		polls  int
	)
	poll := func() {
		head, err := a.workspaceHead(ctx, wsID)
		if err != nil {
			t.Errorf("workspaceHead: %v", err)
			return
		}
		assertContiguousTail(t, pool, wsID, cursor)
		recs, scanned, err := a.collectOutbox(ctx, wsID, reader, cursor, "")
		if err != nil {
			t.Errorf("collectOutbox: %v", err)
			return
		}
		for _, rec := range recs {
			seq := mustSeq(t, rec.SequenceID)
			if seq <= cursor || (len(got) > 0 && seq <= got[len(got)-1]) {
				t.Errorf("delta after %d returned %d out of order", cursor, seq)
			}
			got = append(got, seq)
		}
		cursor = max(cursor, head, scanned)
		polls++
	}
	writersDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(writersDone)
	}()
	for running := true; running; {
		select {
		case <-writersDone:
			running = false
		default:
		}
		poll()
	}
	poll() // the reconnect after the last write

	mu.Lock()
	defer mu.Unlock()
	slices.Sort(visible)
	if committed == 0 || rolledBack == 0 {
		t.Fatalf("committed %d, rolled back %d: want both exercised", committed, rolledBack)
	}

	var n, lo, hi, distinct int64
	if err := pool.QueryRow(ctx, `
		select count(*), coalesce(min(sequence_id), 0), coalesce(max(sequence_id), 0),
		       count(distinct sequence_id)
		from sync_outbox where workspace_id = $1`, wsID).Scan(&n, &lo, &hi, &distinct); err != nil {
		t.Fatalf("outbox stats: %v", err)
	}
	if want := int64(committed); n != want || lo != 1 || hi != want || distinct != want {
		t.Fatalf("outbox holds %d rows spanning %d..%d (%d distinct), want exactly 1..%d",
			n, lo, hi, distinct, want)
	}
	if head, err := a.workspaceHead(ctx, wsID); err != nil || head != int64(committed) {
		t.Fatalf("head = %d (%v), want %d", head, err, committed)
	}
	if !slices.Equal(got, visible) {
		t.Fatalf("reader applied %d records, want the %d visible committed ones", len(got), len(visible))
	}
	if cursor != int64(committed) {
		t.Fatalf("reader cursor = %d, want head %d", cursor, committed)
	}

	// A delta from any watermark returns exactly the visible tail and
	// reports the head, whatever the hidden rows in between.
	for _, after := range []int64{0, 1, int64(committed) / 2, int64(committed) - 1} {
		recs, scanned, err := a.collectOutbox(ctx, wsID, reader, after, "")
		if err != nil {
			t.Fatalf("collectOutbox(%d): %v", after, err)
		}
		var seqs []int64
		for _, rec := range recs {
			seqs = append(seqs, mustSeq(t, rec.SequenceID))
		}
		want := slices.DeleteFunc(slices.Clone(visible), func(s int64) bool { return s <= after })
		if !slices.Equal(seqs, want) || scanned != int64(committed) {
			t.Fatalf("delta after %d: %d records, scanned to %d; want %d records, scanned to %d",
				after, len(seqs), scanned, len(want), committed)
		}
	}
	t.Logf("%d committed, %d rolled back, %d visible to the reader, %d polls",
		committed, rolledBack, len(visible), polls)
}

// assertContiguousTail checks that the committed outbox rows past cursor
// are cursor+1, cursor+2, ... with no hole: what makes a delta complete
// up to the highest sequence it scanned.
func assertContiguousTail(t *testing.T, pool *pgxpool.Pool, workspaceID string, cursor int64) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`select sequence_id from sync_outbox where workspace_id = $1 and sequence_id > $2 order by sequence_id`,
		workspaceID, cursor)
	if err != nil {
		t.Errorf("tail query: %v", err)
		return
	}
	defer rows.Close()
	want := cursor + 1
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Errorf("tail scan: %v", err)
			return
		}
		if seq != want {
			t.Errorf("outbox tail after %d: saw %d, want %d (a hole)", cursor, seq, want)
			return
		}
		want++
	}
	if err := rows.Err(); err != nil {
		t.Errorf("tail rows: %v", err)
	}
}

func mustSeq(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("sequence %q: %v", s, err)
	}
	return n
}

// testUUID renders an RFC-4122-shaped id from crypto/rand.
func testUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
