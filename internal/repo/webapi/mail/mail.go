// Package mail sends the rendered mails over SMTP, or only logs them without SMTP_URL.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_submissionPort = 587
	_implicitTLS    = 465
	_sendTimeout    = 30 * time.Second
)

var (
	errScheme = errors.New("mail: SMTP_URL scheme must be smtp or smtps")
	errTLS    = errors.New("mail: SMTP_URL tls must be none")
)

// SMTP sends mails through the server of SMTP_URL.
type SMTP struct {
	client *gomail.Client
	from   string
}

var _ repo.Mailer = (*SMTP)(nil)

// NewSMTP parses SMTP_URL: smtp:// requires STARTTLS unless ?tls=none allows plain text,
// smtps:// uses implicit TLS; user info signs in.
func NewSMTP(rawURL, from string) (*SMTP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("mail: SMTP_URL: %w", err)
	}

	opts := []gomail.Option{gomail.WithTimeout(_sendTimeout)}

	port := _submissionPort

	switch u.Scheme {
	case "smtp":
		policy, err := tlsPolicy(u.Query().Get("tls"))
		if err != nil {
			return nil, err
		}

		opts = append(opts, gomail.WithTLSPortPolicy(policy))
	case "smtps":
		port = _implicitTLS

		opts = append(opts, gomail.WithSSL())
	default:
		return nil, errScheme
	}

	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return nil, fmt.Errorf("mail: SMTP_URL port: %w", err)
		}
	}

	opts = append(opts, gomail.WithPort(port))

	if user := u.User; user != nil {
		pass, _ := user.Password()

		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(user.Username()), gomail.WithPassword(pass))
	}

	client, err := gomail.NewClient(u.Hostname(), opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}

	return &SMTP{client: client, from: from}, nil
}

func tlsPolicy(value string) (gomail.TLSPolicy, error) {
	switch value {
	case "":
		return gomail.TLSMandatory, nil
	case "none":
		return gomail.NoTLS, nil
	default:
		return gomail.NoTLS, errTLS
	}
}

// Send implements repo.Mailer.
func (s *SMTP) Send(ctx context.Context, m *repo.Mail) error {
	msg := gomail.NewMsg()

	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}

	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("mail: to: %w", err)
	}

	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)

	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}

	return nil
}

// Log only records that a mail would be sent; the body, which holds the link, is not logged.
type Log struct {
	log *slog.Logger
}

var _ repo.Mailer = (*Log)(nil)

// NewLog returns a Log.
func NewLog(log *slog.Logger) *Log {
	return &Log{log: log}
}

// Send implements repo.Mailer.
func (l *Log) Send(ctx context.Context, m *repo.Mail) error {
	l.log.InfoContext(ctx, "mail not sent: SMTP_URL is unset", "email", m.To, "subject", m.Subject)

	return nil
}
