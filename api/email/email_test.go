package email

import (
	"testing"
	"time"
)

// TestMessageSenderInterface asserts that SMTPSender implements MessageSender.
func TestMessageSenderInterface(t *testing.T) {
	var _ MessageSender = (*SMTPSender)(nil)
}

// TestNewSMTPSenderWithOptions asserts that NewSMTPSender accepts timeout options
// and returns a non-nil sender.
func TestNewSMTPSenderWithOptions(t *testing.T) {
	sender := NewSMTPSender(
		"smtp.example.com",
		587,
		"from@example.com",
		"user",
		"pass",
		WithDialTimeout(5*time.Second),
		WithSendTimeout(15*time.Second),
	)
	if sender == nil {
		t.Fatal("NewSMTPSender returned nil")
	}
}

// TestNewSMTPSenderWithoutOptions asserts that NewSMTPSender works without options.
func TestNewSMTPSenderWithoutOptions(t *testing.T) {
	sender := NewSMTPSender(
		"smtp.example.com",
		587,
		"from@example.com",
		"user",
		"pass",
	)
	if sender == nil {
		t.Fatal("NewSMTPSender returned nil")
	}
}
