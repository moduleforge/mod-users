// Package email provides an interface and implementations for sending outbound email.
package email

import (
	"context"
	"fmt"
	"net/smtp"
)

// Sender sends an email message.
type Sender interface {
	Send(ctx context.Context, to, subject, textBody string) error
}

// SMTPSender sends email via a plain SMTP relay.
type SMTPSender struct {
	host string
	port int
	from string
	auth smtp.Auth // nil if no credentials
}

// NewSMTPSender creates an SMTPSender.
// If user and pass are both empty, no SMTP AUTH is attempted.
func NewSMTPSender(host string, port int, from, user, pass string) *SMTPSender {
	var auth smtp.Auth
	if user != "" || pass != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return &SMTPSender{
		host: host,
		port: port,
		from: from,
		auth: auth,
	}
}

// Send delivers a plain-text email to a single recipient.
func (s *SMTPSender) Send(_ context.Context, to, subject, textBody string) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	msg := []byte("To: " + to + "\r\n" +
		"From: " + s.from + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		textBody)

	if err := smtp.SendMail(addr, s.auth, s.from, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp: send to %s: %w", to, err)
	}
	return nil
}

// NoOpSender discards all messages. Intended for tests.
type NoOpSender struct{}

// Send discards the message and returns nil.
func (NoOpSender) Send(_ context.Context, _, _, _ string) error {
	return nil
}
