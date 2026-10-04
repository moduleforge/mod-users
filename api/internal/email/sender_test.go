package email

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	testRecipient = "victim@example.test"
	testReplyTo   = "Support Team <support@example.test>"
	testFrom      = "noreply@example.test"
	// bound is a generous upper limit for "returns promptly" assertions.
	bound = 5 * time.Second
)

// fakeServer is an in-process SMTP relay on 127.0.0.1:0.
type fakeServer struct {
	ln       net.Listener
	rejectRc bool          // reply 550 echoing the recipient to RCPT
	hung     bool          // accept and never speak
	accepted chan struct{} // signalled per accepted connection
	data     chan string   // DATA payloads received

	starttls bool   // advertise STARTTLS and fail the handshake
	authAdv  bool   // advertise AUTH PLAIN
	authCode string // when set, reply to AUTH with this full line
}

type fakeOpt func(*fakeServer)

func withBrokenStartTLS() fakeOpt { return func(fs *fakeServer) { fs.starttls = true } }
func withAuth(reply string) fakeOpt {
	return func(fs *fakeServer) { fs.authAdv = true; fs.authCode = reply }
}

func newFakeServer(t *testing.T, hung, rejectRcpt bool, opts ...fakeOpt) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs := &fakeServer{
		ln:       ln,
		hung:     hung,
		rejectRc: rejectRcpt,
		accepted: make(chan struct{}, 16),
		data:     make(chan string, 16),
	}
	for _, o := range opts {
		o(fs)
	}
	go fs.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return fs
}

func (fs *fakeServer) port() int { return fs.ln.Addr().(*net.TCPAddr).Port }

func (fs *fakeServer) sender(opts ...Option) *SMTPSender {
	return NewSMTPSender("127.0.0.1", fs.port(), testFrom, "", "", opts...)
}

func (fs *fakeServer) serve() {
	for {
		conn, err := fs.ln.Accept()
		if err != nil {
			return
		}
		fs.accepted <- struct{}{}
		go fs.handle(conn)
	}
}

func (fs *fakeServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	if fs.hung {
		// Never speak; return once the client closes the connection.
		_, _ = r.ReadByte()
		return
	}
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			var exts []string
			if fs.starttls {
				exts = append(exts, "STARTTLS")
			}
			if fs.authAdv {
				exts = append(exts, "AUTH PLAIN")
			}
			if len(exts) == 0 {
				write("250 fake")
				break
			}
			write("250-fake")
			for i, e := range exts {
				sep := "-"
				if i == len(exts)-1 {
					sep = " "
				}
				write("250" + sep + e)
			}
		case strings.HasPrefix(cmd, "STARTTLS"):
			write("220 ready")
			// Not a TLS server: send garbage so the client handshake fails.
			_, _ = conn.Write([]byte("this is not a TLS handshake\r\n"))
			return
		case strings.HasPrefix(cmd, "AUTH"):
			write(fs.authCode)
		case strings.HasPrefix(cmd, "MAIL"):
			write("250 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			if fs.rejectRc {
				write("550 5.1.1 " + strings.TrimSpace(line[len("RCPT TO:"):]) + ": Recipient address rejected")
			} else {
				write("250 ok")
			}
		case strings.HasPrefix(cmd, "DATA"):
			write("354 go ahead")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			fs.data <- sb.String()
			write("250 queued")
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 bye")
			return
		default:
			write("500 what")
		}
	}
}

func recvData(t *testing.T, fs *fakeServer) string {
	t.Helper()
	select {
	case d := <-fs.data:
		return d
	case <-time.After(bound):
		t.Fatal("timed out waiting for DATA payload")
		return ""
	}
}

func headerLines(payload string) []string {
	head, _, _ := strings.Cut(payload, "\r\n\r\n")
	return strings.Split(head, "\r\n")
}

func hasHeader(payload, name string) bool {
	for _, l := range headerLines(payload) {
		if strings.HasPrefix(l, name+":") {
			return true
		}
	}
	return false
}

func TestReplyToHeader(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false)
	s := fs.sender()

	if err := s.SendMessage(context.Background(), Message{
		To: testRecipient, Subject: "hi", TextBody: "body", ReplyTo: testReplyTo,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	p := recvData(t, fs)
	if !strings.Contains(p, "\r\nReply-To: "+testReplyTo+"\r\n") {
		t.Errorf("Reply-To header missing in payload:\n%s", p)
	}
	if !strings.Contains(p, "From: "+testFrom+"\r\n") || !strings.HasSuffix(p, "\r\n\r\nbody\r\n") {
		t.Errorf("unexpected payload:\n%s", p)
	}

	if err := s.SendMessage(context.Background(), Message{To: testRecipient, Subject: "hi", TextBody: "body"}); err != nil {
		t.Fatalf("SendMessage (no reply-to): %v", err)
	}
	if p := recvData(t, fs); hasHeader(p, "Reply-To") {
		t.Errorf("Reply-To header present when empty:\n%s", p)
	}

	if err := s.Send(context.Background(), testRecipient, "hi", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if p := recvData(t, fs); hasHeader(p, "Reply-To") {
		t.Errorf("Send produced a Reply-To header:\n%s", p)
	}
}

func TestHungRelayContextDeadline(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := fs.sender().SendMessage(ctx, Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if elapsed := time.Since(start); elapsed > bound {
		t.Errorf("returned after %v, want < %v", elapsed, bound)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	assertAddressFree(t, err)
}

func TestHungRelayContextCancelled(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-fs.accepted // cancel only once the connection is established
		cancel()
	}()

	start := time.Now()
	err := fs.sender().SendMessage(ctx, Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if elapsed := time.Since(start); elapsed > bound {
		t.Errorf("returned after %v, want < %v", elapsed, bound)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	assertAddressFree(t, err)
}

func TestHungRelaySendTimeout(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, true, false)
	start := time.Now()
	err := fs.sender(WithSendTimeout(200*time.Millisecond)).
		SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if elapsed := time.Since(start); elapsed > bound {
		t.Errorf("returned after %v, want < %v", elapsed, bound)
	}
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("err = %v, want a deadline-exceeded timeout", err)
	}
	assertAddressFree(t, err)
}

func TestNonPositiveOptionsKeepDefaults(t *testing.T) {
	t.Parallel()
	s := NewSMTPSender("h", 25, testFrom, "", "", WithDialTimeout(0), WithSendTimeout(-time.Second))
	if s.dialTimeout != defaultDialTimeout || s.sendTimeout != defaultSendTimeout {
		t.Errorf("timeouts = %v/%v, want defaults", s.dialTimeout, s.sendTimeout)
	}
	s = NewSMTPSender("h", 25, testFrom, "", "", WithDialTimeout(time.Second), WithSendTimeout(2*time.Second))
	if s.dialTimeout != time.Second || s.sendTimeout != 2*time.Second {
		t.Errorf("timeouts = %v/%v, want 1s/2s", s.dialTimeout, s.sendTimeout)
	}
}

func TestDialClosedPort(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	s := NewSMTPSender("127.0.0.1", port, testFrom, "", "")
	start := time.Now()
	err = s.SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b", ReplyTo: testReplyTo})
	if elapsed := time.Since(start); elapsed > bound {
		t.Errorf("returned after %v, want < %v", elapsed, bound)
	}
	if err == nil || !strings.HasPrefix(err.Error(), "smtp: dial: ") {
		t.Fatalf("err = %v, want smtp: dial: ...", err)
	}
	assertAddressFree(t, err)
}

func TestRcptRejectionIsAddressFree(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, true)
	err := fs.sender().SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "smtp: rcpt: ") || !strings.Contains(err.Error(), "550") {
		t.Errorf("err = %q, want smtp: rcpt: ... with code 550", err)
	}
	if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "5.1.1") {
		t.Errorf("err = %q leaks the server message", err)
	}
	assertAddressFree(t, err)
}

func TestValidationRejectsWithoutConnecting(t *testing.T) {
	t.Parallel()
	// Port 1 on loopback is closed: a connection attempt would produce a
	// "dial" stage error rather than "validate".
	s := NewSMTPSender("127.0.0.1", 1, testFrom, "", "")
	tests := []struct {
		name string
		msg  Message
	}{
		{"invalid reply-to", Message{To: testRecipient, Subject: "s", TextBody: "b", ReplyTo: "not an address"}},
		{"CRLF in reply-to", Message{To: testRecipient, Subject: "s", TextBody: "b", ReplyTo: "a@example.test\r\nBcc: x@example.test"}},
		{"LF in to", Message{To: "a@example.test\nBcc: x@example.test", Subject: "s", TextBody: "b"}},
		{"CR in subject", Message{To: testRecipient, Subject: "s\rBcc: x@example.test", TextBody: "b"}},
		{"CRLF in subject", Message{To: testRecipient, Subject: "s\r\nBcc: x@example.test", TextBody: "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := s.SendMessage(context.Background(), tc.msg)
			if err == nil || !strings.HasPrefix(err.Error(), "smtp: validate: ") {
				t.Fatalf("err = %v, want smtp: validate: ...", err)
			}
			assertAddressFree(t, err)
		})
	}
}

func TestRecipientMustBeBareAddress(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false)
	for _, to := range []string{"a@x> NOTIFY=NEVER <b", "Name <a@x>", "a@x, b@y", "<a@x>", ""} {
		err := fs.sender().SendMessage(context.Background(), Message{To: to, Subject: "s", TextBody: "b"})
		if err == nil || err.Error() != "smtp: validate: invalid recipient address" {
			t.Errorf("To %q: err = %v, want smtp: validate: invalid recipient address", to, err)
		}
	}
	select {
	case <-fs.accepted:
		t.Error("a connection was made for an invalid recipient")
	default:
	}
}

func TestBodyLineEndingsNormalized(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false)
	body := "a\r.\r\nb\r\r.c\n.d"
	if err := fs.sender().SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: body}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	p := recvData(t, fs)
	_, got, _ := strings.Cut(p, "\r\n\r\n")
	if want := "a\r\n..\r\nb\r\n\r\n..c\r\n..d\r\n"; got != want {
		t.Errorf("body on the wire = %q, want %q", got, want)
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '\r' && (i+1 >= len(p) || p[i+1] != '\n') {
			t.Fatalf("bare CR at offset %d in payload %q", i, p)
		}
	}
}

func TestStartTLSHandshakeFailure(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false, withBrokenStartTLS())
	err := fs.sender().SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if err == nil || !strings.HasPrefix(err.Error(), "smtp: starttls: ") {
		t.Fatalf("err = %v, want smtp: starttls: ...", err)
	}
	assertAddressFree(t, err)
}

func TestAuthNotAdvertised(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false)
	s := NewSMTPSender("127.0.0.1", fs.port(), testFrom, "user", "pass")
	err := s.SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if err == nil || err.Error() != "smtp: auth: server does not support AUTH" {
		t.Fatalf("err = %v, want smtp: auth: server does not support AUTH", err)
	}
}

func TestAuthRejectedShowsOnlyCode(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, false, false, withAuth("535 5.7.8 Bad credentials for user"))
	s := NewSMTPSender("127.0.0.1", fs.port(), testFrom, "user", "pass")
	err := s.SendMessage(context.Background(), Message{To: testRecipient, Subject: "s", TextBody: "b"})
	if err == nil || !strings.HasPrefix(err.Error(), "smtp: auth: ") || !strings.Contains(err.Error(), "535") {
		t.Fatalf("err = %v, want smtp: auth: ... with code 535", err)
	}
	if strings.Contains(err.Error(), "Bad credentials") || strings.Contains(err.Error(), "5.7.8") {
		t.Errorf("err = %q leaks the server message", err)
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("reply error exposes a cause: %v", errors.Unwrap(err))
	}
}

func TestFailRedactsErrorChain(t *testing.T) {
	t.Parallel()
	tx := &transaction{ctx: context.Background(), secrets: []string{testRecipient, testReplyTo, testFrom}}

	leaky := fmt.Errorf("lookup %s: no such host", testRecipient)
	err := tx.fail("dial", leaky)
	if strings.Contains(err.Error(), testRecipient) || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("Error() = %q, want address redacted", err)
	}
	for e := error(err); e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), testRecipient) {
			t.Errorf("Unwrap chain element %q contains the address", e)
		}
	}
	if errors.Is(err, leaky) {
		t.Error("unredacted cause reachable through Unwrap")
	}

	// An address-free error keeps its cause for errors.Is.
	sentinel := errors.New("boom")
	if err := tx.fail("dial", sentinel); !errors.Is(err, sentinel) {
		t.Errorf("address-free cause lost: %v", err)
	}
}

func TestNoOpSenderImplementsInterfaces(t *testing.T) {
	t.Parallel()
	var s Sender = NoOpSender{}
	if err := s.Send(context.Background(), "a", "b", "c"); err != nil {
		t.Errorf("Send: %v", err)
	}
	ms, ok := s.(MessageSender)
	if !ok {
		t.Fatal("NoOpSender does not implement MessageSender")
	}
	if err := ms.SendMessage(context.Background(), Message{}); err != nil {
		t.Errorf("SendMessage: %v", err)
	}
}

func assertAddressFree(t *testing.T, err error) {
	t.Helper()
	for _, a := range []string{testRecipient, "support@example.test", testFrom, "x@example.test"} {
		if strings.Contains(err.Error(), a) {
			t.Errorf("error %q contains address %q", err, a)
		}
	}
}
