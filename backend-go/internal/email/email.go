package email

import (
	"context"
	"log/slog"

	"campus-hive/backend-go/internal/config"
)

type Mail struct {
	To      string
	Subject string
	Text    string
}

type Sender interface {
	Send(ctx context.Context, mail Mail) error
	SendVerification(ctx context.Context, to, verifyURL string) error
	SendPasswordReset(ctx context.Context, to, resetURL string) error
}

const (
	VerificationSubject  = "Verify your Campus Hive email"
	PasswordResetSubject = "Reset your Campus Hive password"
)

func VerificationMail(to, verifyURL string) Mail {
	return Mail{To: to, Subject: VerificationSubject, Text: "Verify your email by visiting: " + verifyURL}
}

func PasswordResetMail(to, resetURL string) Mail {
	return Mail{To: to, Subject: PasswordResetSubject, Text: "Reset your password by visiting: " + resetURL}
}

// LoggerSender is the development adapter. It never logs message bodies because
// they contain raw tokens. When an SMTP host is configured it warns loudly
// instead of pretending delivery succeeded, matching the NestJS service.
type LoggerSender struct {
	logger *slog.Logger
	cfg    config.EmailCfg
}

func NewLoggerSender(cfg config.EmailCfg, logger *slog.Logger) *LoggerSender {
	return &LoggerSender{cfg: cfg, logger: logger}
}

func (s *LoggerSender) configured() bool {
	return s.cfg.Host != ""
}

func (s *LoggerSender) Send(_ context.Context, mail Mail) error {
	if !s.configured() {
		s.logger.Info("dev-email",
			"to", mail.To,
			"subject", mail.Subject,
			"note", "no EMAIL_HOST configured",
		)
		return nil
	}
	s.logger.Warn("email provider not wired; email was NOT delivered",
		"to", mail.To,
		"subject", mail.Subject,
	)
	return nil
}

func (s *LoggerSender) SendVerification(ctx context.Context, to, verifyURL string) error {
	return s.Send(ctx, VerificationMail(to, verifyURL))
}

func (s *LoggerSender) SendPasswordReset(ctx context.Context, to, resetURL string) error {
	return s.Send(ctx, PasswordResetMail(to, resetURL))
}
