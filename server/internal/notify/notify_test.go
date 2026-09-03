package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captured returns a logger that records JSON output into buf.
func captured(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestDriverSelection(t *testing.T) {
	log, _ := captured(t)
	if s := New(Config{}, log); s.Enabled() {
		t.Error("an empty host must select the log driver")
	}
	if s := New(Config{Host: "smtp.example.com"}, log); !s.Enabled() {
		t.Error("a host must select the smtp driver")
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	log, _ := captured(t)
	s := New(Config{}, log)
	if s.cfg.Port != 587 {
		t.Errorf("default port = %d, want 587", s.cfg.Port)
	}
	if s.cfg.From != "no-reply@converge.local" {
		t.Errorf("default from = %q, want no-reply@converge.local", s.cfg.From)
	}
	if s.cfg.TLS != "starttls" {
		t.Errorf("default tls = %q, want starttls", s.cfg.TLS)
	}
}

func TestSendLogDriverEmitsFullMessage(t *testing.T) {
	log, buf := captured(t)
	s := New(Config{}, log)
	err := s.Send(context.Background(), Message{
		To:      "jane@example.com",
		Subject: "demo invited you to Acme on Converge",
		Body:    "open the link\nhttp://localhost:3000/auth/verify?preAuthSessionId=x#CODE",
	})
	if err != nil {
		t.Fatalf("log driver must not fail: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not a single JSON record: %v\n%s", err, buf.String())
	}
	if entry["msg"] != "email (log driver)" {
		t.Errorf("msg = %v, want %q", entry["msg"], "email (log driver)")
	}
	if entry["to"] != "jane@example.com" {
		t.Errorf("to = %v, want jane@example.com", entry["to"])
	}
	if !strings.Contains(entry["body"].(string), "http://localhost:3000/auth/verify") {
		t.Errorf("body must carry the link: %v", entry["body"])
	}
}

func TestInviteMessage(t *testing.T) {
	link := "http://localhost:3000/auth/verify?preAuthSessionId=abc#XYZ"
	m := InviteMessage("jane@acme.dev", "demo", "Acme", link, 15*time.Minute)
	if m.To != "jane@acme.dev" {
		t.Errorf("to = %q", m.To)
	}
	if m.Subject != "demo invited you to Acme on Converge" {
		t.Errorf("subject = %q", m.Subject)
	}
	for _, want := range []string{
		"demo invited you to Acme on Converge.",
		"accept or decline",
		link,
		"about 15 minutes",
		"If this wasn't you, ignore this email.",
	} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
}

func TestInviteMessageEmptyInviterFallsBack(t *testing.T) {
	m := InviteMessage("jane@acme.dev", "  ", "Acme", "http://x", 15*time.Minute)
	if !strings.Contains(m.Subject, "A teammate") {
		t.Errorf("subject = %q, want a fallback inviter", m.Subject)
	}
}

func TestHumanTTL(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "a minute",
		1 * time.Minute:  "1 minute",
		15 * time.Minute: "15 minutes",
		time.Hour:        "1 hour",
		30 * time.Hour:   "30 hours",
		48 * time.Hour:   "2 days",
		720 * time.Hour:  "30 days",
	}
	for d, want := range cases {
		if got := humanTTL(d); got != want {
			t.Errorf("humanTTL(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestWriteMessageHeaders(t *testing.T) {
	var buf bytes.Buffer
	_, err := writeMessage(&buf, "no-reply@converge.local", Message{
		To:      "jane@example.com",
		Subject: "plain subject",
		Body:    "line one\nline two",
	})
	if err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"From: no-reply@converge.local\r\n",
		"To: jane@example.com\r\n",
		"Subject: plain subject\r\n",
		"Message-Id: <",
		">",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 8bit\r\n",
		"\r\n\r\n",
		"line one\nline two\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("data block missing %q:\n%s", want, out)
		}
	}
}

func TestEncodeSubject(t *testing.T) {
	if got := encodeSubject("Hello world"); got != "Hello world" {
		t.Errorf("ascii subject must pass through, got %q", got)
	}
	// em-dash is non-ascii: RFC 2047 folded form (mime emits lowercase
	// "q" — equally valid; accept either case)
	if got := encodeSubject("Invited to Acme — team"); !strings.HasPrefix(got, "=?utf-8?") {
		t.Errorf("non-ascii subject must be encoded, got %q", got)
	}
}
