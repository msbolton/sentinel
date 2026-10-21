package registration

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Mailer sends plain-text notifications.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// MailOptions selects how the mailer secures and authenticates the connection.
type MailOptions struct {
	// Username and Password enable SMTP AUTH PLAIN, which is only ever sent
	// over a TLS connection.
	Username string
	Password string
	// TLS is "starttls" (upgrade required), "tls" (implicit TLS), "none"
	// (plaintext), or empty to upgrade only when the relay offers STARTTLS.
	TLS string
}

const (
	defaultDialTimeout = 10 * time.Second
	defaultMailTimeout = 30 * time.Second
)

// SMTPMailer sends through an SMTP relay: mailhog in dev, a real provider
// (STARTTLS or implicit TLS, with authentication) in production.
type SMTPMailer struct {
	host string
	port string
	from string
	opts MailOptions

	dialTimeout time.Duration
	// deadline bounds the whole exchange once connected.
	deadline time.Duration
	// tlsConfig overrides the TLS settings; tests use it to trust a
	// self-signed certificate.
	tlsConfig *tls.Config
}

// NewSMTPMailer creates a mailer for host:port with the given From header,
// which may include a display name.
func NewSMTPMailer(host, port, from string, opts MailOptions) *SMTPMailer {
	return &SMTPMailer{
		host: host, port: port, from: from, opts: opts,
		dialTimeout: defaultDialTimeout, deadline: defaultMailTimeout,
	}
}

// Send implements Mailer.
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(m.from, "\r\n") {
		return errors.New("smtp: CR or LF in an address")
	}
	fromAddr := m.from
	if parsed, err := mail.ParseAddress(m.from); err == nil {
		fromAddr = parsed.Address
	}
	msg := strings.Join([]string{
		"From: " + m.from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	addr := net.JoinHostPort(m.host, m.port)
	if err := m.deliver(ctx, addr, fromAddr, to, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send to %s via %s: %w", to, addr, err)
	}
	return nil
}

func (m *SMTPMailer) deliver(ctx context.Context, addr, from, to string, msg []byte) error {
	switch m.opts.TLS {
	case "", "starttls", "tls", "none":
	default:
		return fmt.Errorf("unknown MAIL_TLS %q (want starttls, tls or none)", m.opts.TLS)
	}
	tlsCfg := m.tlsConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	}

	dialer := &net.Dialer{Timeout: m.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(m.deadline)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	encrypted := false
	if m.opts.TLS == "tls" {
		conn = tls.Client(conn, tlsCfg)
		encrypted = true
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer c.Close()

	if m.opts.TLS == "starttls" || m.opts.TLS == "" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
			encrypted = true
		} else if m.opts.TLS == "starttls" {
			return errors.New("relay does not offer STARTTLS")
		}
	}

	if m.opts.Username != "" {
		if !encrypted {
			return errors.New("refusing to authenticate over an unencrypted connection")
		}
		if err := c.Auth(smtp.PlainAuth("", m.opts.Username, m.opts.Password, m.host)); err != nil {
			return err
		}
	}

	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// NopMailer discards mail; used when no relay is configured and in tests.
type NopMailer struct{}

// Send implements Mailer.
func (NopMailer) Send(context.Context, string, string, string) error { return nil }
