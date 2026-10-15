package registration

import (
	"context"
	"fmt"
	"net/mail"
	"net/smtp"
	"strings"
)

// Mailer sends plain-text notifications.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPMailer sends through an unauthenticated SMTP relay (mailhog in dev).
type SMTPMailer struct {
	addr string
	from string
}

// NewSMTPMailer creates a mailer for host:port with the given From header,
// which may include a display name.
func NewSMTPMailer(host, port, from string) *SMTPMailer {
	return &SMTPMailer{addr: host + ":" + port, from: from}
}

// Send implements Mailer.
func (m *SMTPMailer) Send(_ context.Context, to, subject, body string) error {
	fromAddr := m.from
	if parsed, err := mail.ParseAddress(m.from); err == nil {
		fromAddr = parsed.Address
	}
	msg := strings.Join([]string{
		"From: " + m.from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	if err := smtp.SendMail(m.addr, nil, fromAddr, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send to %s via %s: %w", to, m.addr, err)
	}
	return nil
}

// NopMailer discards mail; used when no relay is configured and in tests.
type NopMailer struct{}

// Send implements Mailer.
func (NopMailer) Send(context.Context, string, string, string) error { return nil }
