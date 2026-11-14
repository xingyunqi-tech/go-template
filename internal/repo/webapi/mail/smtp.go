package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

const _smtpTimeout = 30 * time.Second

// Sender delivers a rendered mail.
type Sender interface {
	Send(ctx context.Context, m *Outgoing) error
}

// SMTPConfig follows the nodemailer transport options of nestjs MailerService.
type SMTPConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	DefaultEmail string
	DefaultName  string
	// Secure is implicit TLS; IgnoreTLS skips STARTTLS; RequireTLS demands it.
	Secure     bool
	IgnoreTLS  bool
	RequireTLS bool
}

// SMTP sends mail with the nodemailer TLS semantics.
type SMTP struct {
	cfg SMTPConfig
}

// NewSMTP returns an SMTP sender.
func NewSMTP(cfg *SMTPConfig) *SMTP {
	return &SMTP{cfg: *cfg}
}

func (s *SMTP) options() []gomail.Option {
	opts := []gomail.Option{gomail.WithPort(s.cfg.Port), gomail.WithTimeout(_smtpTimeout)}

	switch {
	case s.cfg.Secure:
		opts = append(opts, gomail.WithSSL())
	case s.cfg.IgnoreTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	case s.cfg.RequireTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	}

	if s.cfg.User != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.User),
			gomail.WithPassword(s.cfg.Password),
		)
	}

	return opts
}

// Message builds the MIME message: text and HTML alternatives from "<name>" <email>.
func (s *SMTP) Message(m *Outgoing) (*gomail.Msg, error) {
	msg := gomail.NewMsg()

	if err := msg.FromFormat(s.cfg.DefaultName, s.cfg.DefaultEmail); err != nil {
		return nil, fmt.Errorf("mail: from: %w", err)
	}

	if err := msg.To(m.To); err != nil {
		return nil, fmt.Errorf("mail: to: %w", err)
	}

	msg.Subject(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(m.Subject))
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	msg.SetDate()
	msg.SetMessageID()

	return msg, nil
}

// Send delivers m.
func (s *SMTP) Send(ctx context.Context, m *Outgoing) error {
	msg, err := s.Message(m)
	if err != nil {
		return err
	}

	client, err := gomail.NewClient(s.cfg.Host, s.options()...)
	if err != nil {
		return fmt.Errorf("mail: smtp client: %w", err)
	}

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}

	return nil
}

// Log writes mail to the log instead of sending it, for environments without MAIL_HOST.
type Log struct {
	log *slog.Logger
}

// NewLog returns a Log sender.
func NewLog(log *slog.Logger) *Log {
	return &Log{log: log}
}

// Send logs the recipient, masked like nestjs maskEmail, and the subject.
func (l *Log) Send(ctx context.Context, m *Outgoing) error {
	l.log.InfoContext(ctx, "mail not sent: MAIL_HOST is not set", "to", MaskEmail(m.To), "subject", m.Subject)

	return nil
}

// MaskEmail is nestjs maskEmail: the first two characters of the local part (one when it is that
// short), then *** and the domain.
func MaskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		return "[invalid]"
	}

	local, domain := []rune(email[:at]), email[at:]

	const visible = 2

	if len(local) <= visible {
		return string(local[:1]) + "***" + domain
	}

	return string(local[:visible]) + "***" + domain
}
