// Package email provides an interface and implementations for sending outbound email.
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDialTimeout = 10 * time.Second
	defaultSendTimeout = 30 * time.Second
)

// Sender sends an email message.
//
// Implementations must honor ctx for cancellation and deadlines, and must not
// include the recipient (or any other message address) in returned errors, so
// that errors are safe to log.
type Sender interface {
	Send(ctx context.Context, to, subject, textBody string) error
}

// Message is a single plain-text email to one recipient.
type Message struct {
	// To is the recipient address.
	To string
	// Subject is the message subject.
	Subject string
	// TextBody is the plain-text body.
	TextBody string
	// ReplyTo, when non-empty, is emitted as the Reply-To header. It must be a
	// valid RFC 5322 address. The From address is fixed by the sender.
	ReplyTo string
}

// MessageSender is implemented by senders that support the full Message,
// including Reply-To. Callers holding a Sender may type-assert to it.
type MessageSender interface {
	SendMessage(ctx context.Context, msg Message) error
}

var (
	_ Sender        = (*SMTPSender)(nil)
	_ MessageSender = (*SMTPSender)(nil)
	_ Sender        = NoOpSender{}
	_ MessageSender = NoOpSender{}
)

// SMTPSender sends email via an SMTP relay. STARTTLS is used when the relay
// advertises it, and SMTP AUTH is used when credentials were configured.
//
// The context passed to SendMessage bounds the whole transaction: it limits
// dialing, applies its deadline to every network read and write, and closes the
// connection promptly when cancelled. Returned errors never contain the
// recipient, Reply-To, or From address.
type SMTPSender struct {
	host        string
	port        int
	from        string
	auth        smtp.Auth // nil if no credentials
	dialTimeout time.Duration
	sendTimeout time.Duration
}

// Option configures an SMTPSender.
type Option func(*SMTPSender)

// WithDialTimeout sets the maximum time to establish the TCP connection
// (default 10s). Non-positive values leave the default.
func WithDialTimeout(d time.Duration) Option {
	return func(s *SMTPSender) {
		if d > 0 {
			s.dialTimeout = d
		}
	}
}

// WithSendTimeout sets the overall cap for the whole SMTP transaction,
// including dialing (default 30s). The context deadline still applies if it is
// earlier. Non-positive values leave the default.
func WithSendTimeout(d time.Duration) Option {
	return func(s *SMTPSender) {
		if d > 0 {
			s.sendTimeout = d
		}
	}
}

// NewSMTPSender creates an SMTPSender.
// If user and pass are both empty, no SMTP AUTH is attempted.
func NewSMTPSender(host string, port int, from, user, pass string, opts ...Option) *SMTPSender {
	var auth smtp.Auth
	if user != "" || pass != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	s := &SMTPSender{
		host:        host,
		port:        port,
		from:        from,
		auth:        auth,
		dialTimeout: defaultDialTimeout,
		sendTimeout: defaultSendTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Send delivers a plain-text email to a single recipient, without Reply-To. It
// is equivalent to SendMessage with an empty ReplyTo.
func (s *SMTPSender) Send(ctx context.Context, to, subject, textBody string) error {
	return s.SendMessage(ctx, Message{To: to, Subject: subject, TextBody: textBody})
}

// SendMessage delivers msg. See SMTPSender for what ctx controls. When ctx is
// done the returned error satisfies errors.Is(err, ctx.Err()). Errors have the
// form "smtp: <stage>: ..." and never contain the To, ReplyTo, or From
// addresses; relay replies are reduced to their numeric code.
func (s *SMTPSender) SendMessage(ctx context.Context, msg Message) error {
	for _, v := range []string{msg.To, msg.Subject, msg.ReplyTo} {
		if strings.ContainsAny(v, "\r\n") {
			return &sendError{stage: "validate", detail: "header value contains line break"}
		}
	}
	// To must be a bare addr-spec: it is used verbatim as the RCPT TO argument
	// and the To header, so a display name, angle brackets, or an address list
	// would be passed through to the relay.
	if a, err := mail.ParseAddress(msg.To); err != nil || a.Address != msg.To {
		return &sendError{stage: "validate", detail: "invalid recipient address"}
	}
	if msg.ReplyTo != "" {
		if _, err := mail.ParseAddress(msg.ReplyTo); err != nil {
			return &sendError{stage: "validate", detail: "invalid reply-to address"}
		}
	}

	var b strings.Builder
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("From: " + s.from + "\r\n")
	if msg.ReplyTo != "" {
		b.WriteString("Reply-To: " + msg.ReplyTo + "\r\n")
	}
	b.WriteString("Subject: " + msg.Subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		normalizeLineEndings(msg.TextBody))

	t := &transaction{
		s:        s,
		ctx:      ctx,
		secrets:  []string{msg.To, msg.ReplyTo, s.from},
		deadline: time.Now().Add(s.sendTimeout),
	}
	if d, ok := ctx.Deadline(); ok && d.Before(t.deadline) {
		t.deadline = d
		t.ctxDeadline = d
	}
	return t.run(msg.To, []byte(b.String()))
}

// normalizeLineEndings converts CRLF and bare CR to LF so the DATA writer's
// CRLF conversion and dot-stuffing apply uniformly and no bare CR reaches the
// wire.
func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// transaction holds the state of one SendMessage call.
type transaction struct {
	s           *SMTPSender
	ctx         context.Context
	secrets     []string  // addresses that must never appear in errors
	deadline    time.Time // earlier of ctx deadline and now+sendTimeout
	ctxDeadline time.Time // zero unless ctx's own deadline is the effective one
}

func (t *transaction) run(to string, msg []byte) (err error) {
	addr := net.JoinHostPort(t.s.host, strconv.Itoa(t.s.port))
	dialer := net.Dialer{Timeout: t.s.dialTimeout, Deadline: t.deadline}
	conn, err := dialer.DialContext(t.ctx, "tcp", addr)
	if err != nil {
		return t.fail("dial", err)
	}
	// Close the connection promptly on cancellation; the watcher always exits
	// before run returns.
	stop := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-t.ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-exited
		_ = conn.Close()
	}()

	if err := conn.SetDeadline(t.deadline); err != nil {
		return t.fail("dial", err)
	}

	c, err := smtp.NewClient(conn, t.s.host)
	if err != nil {
		return t.fail("dial", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello("localhost"); err != nil {
		return t.fail("dial", err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: t.s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return t.fail("starttls", err)
		}
	}
	if t.s.auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return &sendError{stage: "auth", detail: "server does not support AUTH"}
		}
		if err := c.Auth(t.s.auth); err != nil {
			return t.fail("auth", err)
		}
	}
	if err := c.Mail(t.s.from); err != nil {
		return t.fail("mail", err)
	}
	if err := c.Rcpt(to); err != nil {
		return t.fail("rcpt", err)
	}
	w, err := c.Data()
	if err != nil {
		return t.fail("data", err)
	}
	if _, err := w.Write(msg); err != nil {
		return t.fail("data", err)
	}
	if err := w.Close(); err != nil {
		return t.fail("data", err)
	}
	if err := c.Quit(); err != nil {
		return t.fail("quit", err)
	}
	return nil
}

// fail converts err from the given stage into an address-free error.
func (t *transaction) fail(stage string, err error) error {
	// Context done: report the context error (the I/O error is just fallout
	// from closing the connection or hitting the deadline).
	if cerr := t.ctx.Err(); cerr != nil {
		return &sendError{stage: stage, detail: cerr.Error(), cause: cerr}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() && !t.ctxDeadline.IsZero() && !time.Now().Before(t.ctxDeadline) {
		return &sendError{stage: stage, detail: context.DeadlineExceeded.Error(), cause: context.DeadlineExceeded}
	}
	// Relay replies commonly echo addresses; keep only the numeric code.
	var tpe *textproto.Error
	if errors.As(err, &tpe) {
		return &sendError{stage: stage, detail: fmt.Sprintf("server replied %d", tpe.Code)}
	}
	detail := err.Error()
	for _, s := range t.secrets {
		if s != "" {
			detail = strings.ReplaceAll(detail, s, "[redacted]")
		}
	}
	// The unredacted error text may carry an address; expose the cause through
	// Unwrap only when it carries none, so the chain is address-free too.
	var cause error = err
	raw := err.Error()
	for _, s := range t.secrets {
		if s != "" && strings.Contains(raw, s) {
			cause = nil
			break
		}
	}
	return &sendError{stage: stage, detail: detail, cause: cause}
}

// sendError is an SMTP failure whose text never contains message addresses.
// cause is exposed through Unwrap only for non-reply errors (context and
// network errors) whose own text carries no message address, so the Unwrap
// chain is address-free and callers can use errors.Is/As on it.
type sendError struct {
	stage  string
	detail string
	cause  error
}

func (e *sendError) Error() string { return "smtp: " + e.stage + ": " + e.detail }
func (e *sendError) Unwrap() error { return e.cause }

// NoOpSender discards all messages. Intended for tests.
type NoOpSender struct{}

// Send discards the message and returns nil.
func (NoOpSender) Send(_ context.Context, _, _, _ string) error {
	return nil
}

// SendMessage discards the message and returns nil.
func (NoOpSender) SendMessage(_ context.Context, _ Message) error {
	return nil
}
