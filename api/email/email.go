// Package email is the public facade for users-module email sending.
// It re-exports the SMTPSender type and constructor from internal/email.
package email

import inner "github.com/moduleforge/mod-users/api/internal/email"

// SMTPSender sends transactional emails via SMTP.
type SMTPSender = inner.SMTPSender

// Sender is the email-sender interface.
type Sender = inner.Sender

// NewSMTPSender constructs an SMTPSender from connection parameters.
func NewSMTPSender(host string, port int, from, user, pass string) *SMTPSender {
	return inner.NewSMTPSender(host, port, from, user, pass)
}
