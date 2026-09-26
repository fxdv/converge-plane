package main

import (
	"bufio"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// mailbox is the probe's SMTP sink: the API under test delivers sign-in
// mail here (CONVERGE_SMTP_TLS=off), and checks read links from it the
// way a user's inbox would.
type mailbox struct {
	mu      sync.Mutex
	byRcpt  map[string][]string
	arrived chan struct{}
}

func newMailbox() *mailbox {
	return &mailbox{byRcpt: map[string][]string{}, arrived: make(chan struct{}, 1)}
}

// serve accepts SMTP sessions until ln closes. It speaks just enough of
// RFC 5321 for net/smtp: greeting, EHLO/HELO, MAIL, RCPT, DATA, RSET,
// NOOP, QUIT.
func (m *mailbox) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go m.session(conn)
	}
}

func (m *mailbox) session(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = fmt.Fprintf(conn, "%s\r\n", s) }
	reply("220 authprobe ESMTP")
	var rcpts []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO":
			reply("250-authprobe")
			reply("250 8BITMIME")
		case "HELO", "NOOP":
			reply("250 OK")
		case "MAIL":
			rcpts = nil
			reply("250 OK")
		case "RCPT":
			addr := line[strings.Index(line, ":")+1:]
			rcpts = append(rcpts, strings.ToLower(strings.Trim(strings.TrimSpace(addr), "<>")))
			reply("250 OK")
		case "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				l = strings.TrimRight(l, "\r\n")
				if l == "." {
					break
				}
				body.WriteString(strings.TrimPrefix(l, "."))
				body.WriteString("\n")
			}
			m.deliver(rcpts, body.String())
			reply("250 OK")
		case "RSET":
			rcpts = nil
			reply("250 OK")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func (m *mailbox) deliver(rcpts []string, msg string) {
	m.mu.Lock()
	for _, rc := range rcpts {
		m.byRcpt[rc] = append(m.byRcpt[rc], msg)
	}
	m.mu.Unlock()
	select {
	case m.arrived <- struct{}{}:
	default:
	}
}

// wait returns the n-th message (1-based) delivered to rcpt, or an error
// after timeout: sign-in mail is sent off the request path.
func (m *mailbox) wait(rcpt string, n int, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	for {
		m.mu.Lock()
		msgs := m.byRcpt[strings.ToLower(rcpt)]
		m.mu.Unlock()
		if len(msgs) >= n {
			return msgs[n-1], nil
		}
		select {
		case <-m.arrived:
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			return "", fmt.Errorf("no mail #%d for %s within %s (is the API's CONVERGE_SMTP_* pointed at the probe?)", n, rcpt, timeout)
		}
	}
}

// count reports how many messages rcpt has received.
func (m *mailbox) count(rcpt string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byRcpt[strings.ToLower(rcpt)])
}

var linkPattern = regexp.MustCompile(`(https?://\S+?)/auth/verify\?preAuthSessionId=([0-9A-Za-z_-]+)#([0-9A-Za-z_-]+)`)

// signInLink is a parsed magic link.
type signInLink struct {
	origin, preAuthSessionID, code string
}

func parseLink(msg string) (signInLink, error) {
	m := linkPattern.FindStringSubmatch(msg)
	if m == nil {
		return signInLink{}, fmt.Errorf("no sign-in link in mail: %q", msg)
	}
	return signInLink{origin: m[1], preAuthSessionID: m[2], code: m[3]}, nil
}
