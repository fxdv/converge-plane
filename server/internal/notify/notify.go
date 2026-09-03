// Package notify delivers transactional mail. v1 has one message type
// (workspace invitations) and two drivers:
//
//   - "smtp": real delivery over the SMTP protocol, stdlib only
//     (net/smtp + crypto/tls). Selected when Config.Host is set.
//   - "log":  the default when no SMTP host is configured — the full
//     message (recipient, subject, body) is written to the service log
//     so a deployment without an email provider stays invite-able and
//     debuggable (the invite link is in the log line).
//
// Messages are plain text. A richer driver (HTML, templating) is a third
// implementation of the same Send seam when the product grows one.
package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is a single outgoing email. The From address comes from the
// service configuration, never from the message.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Config selects and configures the driver. An empty Host selects the
// log driver.
type Config struct {
	// Host is the SMTP server hostname or IP.
	Host string
	// Port is the SMTP port (default 587).
	Port int
	// User and Pass authenticate to the server when set (PLAIN auth).
	User string
	Pass string
	// From is the From: address (default no-reply@converge.local).
	From string
	// TLS selects the transport: "starttls" (default), "off", or
	// "implicit" (TLS from the first byte, the port-465 style).
	TLS string
}

// Service sends messages with the driver named by its configuration.
type Service struct {
	cfg Config
	log *slog.Logger
}

// New applies defaults and returns a ready Service.
func New(cfg Config, log *slog.Logger) *Service {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if strings.TrimSpace(cfg.From) == "" {
		cfg.From = "no-reply@converge.local"
	}
	if strings.TrimSpace(cfg.TLS) == "" {
		cfg.TLS = "starttls"
	}
	return &Service{cfg: cfg, log: log}
}

// Enabled reports whether real SMTP delivery is configured (false = log
// driver).
func (s *Service) Enabled() bool { return s.cfg.Host != "" }

// Send delivers msg on the configured driver. The log driver never
// fails; the SMTP driver reports connection, authentication, and
// protocol errors for the caller to log and drop (invitations are
// best-effort: the row is committed before mail is attempted).
func (s *Service) Send(ctx context.Context, msg Message) error {
	if !s.Enabled() {
		s.log.Info("email (log driver)", "to", msg.To, "subject", msg.Subject, "body", msg.Body)
		return nil
	}
	return s.sendSMTP(ctx, msg)
}

// InviteMessage renders the workspace invitation email. The app name is
// hardcoded: v1 has no per-deployment branding seam.
func InviteMessage(to, inviterName, workspaceName, link string, codeTTL time.Duration) Message {
	if strings.TrimSpace(inviterName) == "" {
		inviterName = "A teammate"
	}
	return Message{
		To:      to,
		Subject: fmt.Sprintf("%s invited you to %s on Converge", inviterName, workspaceName),
		Body: fmt.Sprintf(
			"%s invited you to %s on Converge.\n\n"+
				"Open this link to sign in, then accept or decline the invitation:\n\n"+
				"  %s\n\n"+
				"The link expires in about %s. If this wasn't you, ignore this email.",
			inviterName, workspaceName, link, humanTTL(codeTTL)),
	}
}

// humanTTL renders a TTL in the units a reader expects.
func humanTTL(d time.Duration) string {
	mins := int(d.Minutes())
	switch {
	case mins < 1:
		return "a minute"
	case mins == 1:
		return "1 minute"
	case mins < 60:
		return fmt.Sprintf("%d minutes", mins)
	case int(d.Hours()) == 1:
		return "1 hour"
	case int(d.Hours()) >= 48:
		// 24-47 h stay in hours ("1 day" for 30 h would mislead).
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// sendSMTP opens one connection per message. v1 volumes (a handful of
// invitations per batch) never justify a pooled client, and one
// connection per send keeps timeout handling trivial: the caller's
// context deadline is applied to the socket, bounding every phase of
// the SMTP exchange.
func (s *Service) sendSMTP(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	d := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	switch strings.ToLower(s.cfg.TLS) {
	case "implicit":
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: s.cfg.Host})
	default: // "starttls" and "off" share the plain dial
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake with %s: %w", s.cfg.Host, err)
	}
	defer func() { _ = client.Close() }()

	if strings.ToLower(s.cfg.TLS) == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
				return fmt.Errorf("starttls with %s: %w", s.cfg.Host, err)
			}
		}
		// No STARTTLS advertised: proceed to the host the operator
		// named (the typical localhost-relay layout).
	}

	if s.cfg.User != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp server %s does not advertise AUTH", s.cfg.Host)
		}
		if err := client.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth with %s: %w", s.cfg.Host, err)
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM %s: %w", s.cfg.From, err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp RCPT TO %s: %w", msg.To, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data open: %w", err)
	}
	if _, err := writeMessage(w, s.cfg.From, msg); err != nil {
		return fmt.Errorf("smtp data write: %w", err)
	}
	return w.Close()
}

// writeMessage renders the RFC 5321 data block: CRLF-terminated headers
// and an 8-bit UTF-8 body. A non-ASCII subject is folded per RFC 2047.
func writeMessage(w io.Writer, from string, m Message) (int, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeSubject(m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&b, "Message-Id: <%s>\r\n", messageID())
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(m.Body)
	// The body must end in a line break: the ".." terminator that
	// net/smtp appends on close opens a new line, so a missing one
	// would glue the terminator onto the last body line.
	b.WriteString("\r\n")
	return io.WriteString(w, b.String())
}

// encodeSubject folds a non-ASCII subject per RFC 2047; pure printable
// ASCII passes through untouched.
func encodeSubject(v string) string {
	for _, r := range v {
		if r < 32 || r >= 127 {
			return mime.QEncoding.Encode("utf-8", v)
		}
	}
	return v
}

// messageID returns a globally unique RFC 5321 Message-Id local part.
func messageID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		b = sum[:8] // crypto failure degrades gracefully: time still uniques
	}
	return fmt.Sprintf("%x.%x@converge", sum[:16], b)
}
