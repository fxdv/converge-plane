package main

import (
	"net"
	"net/smtp"
	"testing"
	"time"
)

// TestMailboxReceivesSignInLink drives the sink with net/smtp, the client
// the server's mailer uses, and parses the link out of a dot-stuffed body.
func TestMailboxReceivesSignInLink(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	mb := newMailbox()
	go mb.serve(ln)

	body := "Subject: sign in\r\n\r\nOpen:\r\n\r\n  https://app.example.com/auth/verify?preAuthSessionId=ab12cd#CODE-1_X\r\n..and a dot-led line\r\n"
	if err := smtp.SendMail(ln.Addr().String(), nil, "from@example.com", []string{"User@Example.com"}, []byte(body)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	msg, err := mb.wait("user@example.com", 1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	link, err := parseLink(msg)
	if err != nil {
		t.Fatal(err)
	}
	if link.origin != "https://app.example.com" || link.preAuthSessionID != "ab12cd" || link.code != "CODE-1_X" {
		t.Fatalf("link = %+v", link)
	}
	if mb.count("USER@example.com") != 1 {
		t.Fatalf("count = %d, want 1", mb.count("user@example.com"))
	}
	if _, err := mb.wait("user@example.com", 2, 100*time.Millisecond); err == nil {
		t.Fatal("wait for a second message returned without one")
	}
}
