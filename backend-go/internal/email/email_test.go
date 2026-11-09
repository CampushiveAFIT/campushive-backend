package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"campus-hive/backend-go/internal/config"
)

func capture(t *testing.T, level slog.Level, cfg config.EmailCfg) (*LoggerSender, *bytes.Buffer) {
	t.Helper()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: level}))
	return NewLoggerSender(cfg, logger), &buffer
}

func TestVerificationAndResetMailTemplates(t *testing.T) {
	verify := VerificationMail("a@b.test", "https://app.test/verify?token=raw-verify-token")
	if verify.To != "a@b.test" {
		t.Errorf("to = %q", verify.To)
	}
	if verify.Subject != "Verify your Campus Hive email" {
		t.Errorf("subject = %q", verify.Subject)
	}
	if verify.Text != "Verify your email by visiting: https://app.test/verify?token=raw-verify-token" {
		t.Errorf("text = %q", verify.Text)
	}

	reset := PasswordResetMail("a@b.test", "https://app.test/reset?token=raw-reset-token")
	if reset.Subject != "Reset your Campus Hive password" {
		t.Errorf("subject = %q", reset.Subject)
	}
	if reset.Text != "Reset your password by visiting: https://app.test/reset?token=raw-reset-token" {
		t.Errorf("text = %q", reset.Text)
	}
}

func TestLoggerSenderNeverLogsRawTokens(t *testing.T) {
	sender, buffer := capture(t, slog.LevelDebug, config.EmailCfg{})

	if err := sender.SendVerification(context.Background(), "a@b.test", "https://app.test/verify?token=raw-verify-token"); err != nil {
		t.Fatalf("SendVerification: %v", err)
	}
	if err := sender.SendPasswordReset(context.Background(), "a@b.test", "https://app.test/reset?token=raw-reset-token"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}

	logged := buffer.String()
	if strings.Contains(logged, "raw-verify-token") || strings.Contains(logged, "raw-reset-token") {
		t.Errorf("the raw token leaked into the log: %s", logged)
	}
	if !strings.Contains(logged, "dev-email") {
		t.Errorf("log = %s", logged)
	}
	if !strings.Contains(logged, "to=a@b.test") {
		t.Errorf("the recipient must be logged: %s", logged)
	}
	if !strings.Contains(logged, "no EMAIL_HOST configured") {
		t.Errorf("log = %s", logged)
	}
}

func TestConfiguredSenderWarnsThatNothingWasDelivered(t *testing.T) {
	sender, buffer := capture(t, slog.LevelDebug, config.EmailCfg{Host: "smtp.test", Port: 587})

	if err := sender.Send(context.Background(), VerificationMail("a@b.test", "https://app.test/verify?token=raw")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	logged := buffer.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("a configured provider must warn loudly: %s", logged)
	}
	if !strings.Contains(logged, "email was NOT delivered") {
		t.Errorf("log = %s", logged)
	}
	if strings.Contains(logged, "token=raw") {
		t.Errorf("the raw token leaked into the log: %s", logged)
	}
}

func TestLoggerSenderSatisfiesTheSenderInterface(t *testing.T) {
	sender, _ := capture(t, slog.LevelDebug, config.EmailCfg{})
	var iface Sender = sender
	if iface.SendVerification(context.Background(), "a@b.test", "https://app.test/verify?token=x") != nil {
		t.Error("SendVerification must not fail")
	}
}
