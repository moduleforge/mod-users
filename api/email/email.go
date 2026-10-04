// Package email is the public facade for users-module email sending.
// It re-exports types and constructors from internal/email, including support
// for configurable timeouts via WithDialTimeout and WithSendTimeout, and
// messages with Reply-To headers via Message and MessageSender.
package email

import (
	"time"

	inner "github.com/moduleforge/mod-users/api/internal/email"
)

// SMTPSender sends transactional emails via SMTP.
type SMTPSender = inner.SMTPSender

// Sender is the email-sender interface.
type Sender = inner.Sender

// Message is a single plain-text email to one recipient.
type Message = inner.Message

// MessageSender is implemented by senders that support the full Message,
// including Reply-To. Callers holding a Sender may type-assert to it.
type MessageSender = inner.MessageSender

// Option configures an SMTPSender.
type Option = inner.Option

// WithDialTimeout sets the maximum time to establish the TCP connection
// (default 10s). Non-positive values leave the default.
func WithDialTimeout(d time.Duration) Option {
	return inner.WithDialTimeout(d)
}

// WithSendTimeout sets the overall cap for the whole SMTP transaction,
// including dialing (default 30s). The context deadline still applies if it is
// earlier. Non-positive values leave the default.
func WithSendTimeout(d time.Duration) Option {
	return inner.WithSendTimeout(d)
}

// NewSMTPSender constructs an SMTPSender from connection parameters.
// opts can include WithDialTimeout and WithSendTimeout to configure timeouts.
// If user and pass are both empty, no SMTP AUTH is attempted.
func NewSMTPSender(host string, port int, from, user, pass string, opts ...Option) *SMTPSender {
	return inner.NewSMTPSender(host, port, from, user, pass, opts...)
}
