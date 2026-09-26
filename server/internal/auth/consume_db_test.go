package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/notify"
)

// recordingMailer captures sent messages for assertions.
type recordingMailer struct {
	mu   sync.Mutex
	sent []notify.Message
	done chan struct{}
}

func newRecordingMailer() *recordingMailer {
	return &recordingMailer{done: make(chan struct{}, 16)}
}

func (m *recordingMailer) Send(_ context.Context, msg notify.Message) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	m.done <- struct{}{}
	return nil
}

func (m *recordingMailer) wait(t *testing.T) notify.Message {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no sign-in mail sent")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

// dbService returns a Service on CONVERGE_TEST_DATABASE_URL (skipping
// without one) and a throwaway human email whose rows are removed after
// the test.
func dbService(t *testing.T) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	s, pool, closeFn, err := testServiceWithDB(t, dbURL)
	if err != nil {
		t.Fatalf("test db: %v", err)
	}
	email := hex.EncodeToString([]byte(randomUUID()))[:16] + "@test.local"
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `delete from auth_codes where email = $1`, email)
		_, _ = pool.Exec(ctx, `delete from accounts where email = $1`, email)
		closeFn()
	})
	return s, pool, email
}

func consume(t *testing.T, s *Service, code, preAuth string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"linkCode": code, "preAuthSessionId": preAuth})
	rec := httptest.NewRecorder()
	s.handleConsumeCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code/consume", bytes.NewReader(body)))
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("consume response %d %q: %v", rec.Code, rec.Body.String(), err)
	}
	return out.Status
}

func TestConsumeCodeIsSingleUse(t *testing.T) {
	s, _, email := dbService(t)
	code, preAuth, _, err := s.issueCode(context.Background(), email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	if got := consume(t, s, code, preAuth); got != "OK" {
		t.Fatalf("first consume = %q, want OK", got)
	}
	if got := consume(t, s, code, preAuth); got != "INVALID_LINK_CODE" {
		t.Fatalf("second consume = %q, want INVALID_LINK_CODE (a code works once)", got)
	}
}

func TestConsumeCodeConcurrentHasOneWinner(t *testing.T) {
	s, _, email := dbService(t)
	code, preAuth, _, err := s.issueCode(context.Background(), email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	const racers = 8
	results := make(chan string, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- consume(t, s, code, preAuth)
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for r := range results {
		if r == "OK" {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent consumers succeeded, want exactly 1", ok)
	}
}

func TestConsumeBurnsOlderCodesForTheAddress(t *testing.T) {
	s, _, email := dbService(t)
	ctx := context.Background()
	older, olderPre, _, err := s.issueCode(ctx, email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	newer, newerPre, _, err := s.issueCode(ctx, email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	if got := consume(t, s, newer, newerPre); got != "OK" {
		t.Fatalf("consume newer = %q", got)
	}
	if got := consume(t, s, older, olderPre); got != "INVALID_LINK_CODE" {
		t.Fatalf("older link after sign-in = %q, want INVALID_LINK_CODE", got)
	}
}

func TestConsumeExpiredCode(t *testing.T) {
	s, pool, email := dbService(t)
	ctx := context.Background()
	code, preAuth, _, err := s.issueCode(ctx, email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	if _, err := pool.Exec(ctx, `update auth_codes set expires_at = now() - interval '1 minute' where token_hash = $1`, hashValue(code)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := consume(t, s, code, preAuth); got != "EXPIRED_LINK_CODE" {
		t.Fatalf("expired consume = %q, want EXPIRED_LINK_CODE", got)
	}
}

func TestResendCannotReviveConsumedCode(t *testing.T) {
	s, _, email := dbService(t)
	code, preAuth, _, err := s.issueCode(context.Background(), email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	if got := consume(t, s, code, preAuth); got != "OK" {
		t.Fatalf("consume = %q", got)
	}
	body, _ := json.Marshal(map[string]string{"preAuthSessionId": preAuth})
	rec := httptest.NewRecorder()
	s.handleResendCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code/resend", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "EXPIRED_PRE_AUTH_SESSION") {
		t.Fatalf("resend after consume = %d %s, want 400 EXPIRED_PRE_AUTH_SESSION", rec.Code, rec.Body.String())
	}
}

func TestCreateCodeMailsLinkAndNeverLogsIt(t *testing.T) {
	s, pool, email := dbService(t)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewJSONHandler(&logs, nil))
	mailer := newRecordingMailer()
	s.mailer = mailer
	s.cfg.WebOrigin = "http://localhost:3000"

	body, _ := json.Marshal(map[string]string{"email": email})
	rec := httptest.NewRecorder()
	s.handleCreateCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create code = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "devMagicLink") {
		t.Fatal("devMagicLink returned outside dev mode")
	}
	msg := mailer.wait(t)
	if msg.To != email || !strings.Contains(msg.Body, "/auth/verify?preAuthSessionId=") {
		t.Fatalf("sign-in mail: %+v", msg)
	}
	code := msg.Body[strings.Index(msg.Body, "#")+1:]
	code = strings.Fields(code)[0]
	if strings.Contains(logs.String(), code) {
		t.Fatalf("the sign-in code reached the log: %s", logs.String())
	}
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from auth_codes where token_hash = $1`, hashValue(code)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("mailed code is not the stored code: n=%d err=%v", n, err)
	}
}
