//go:build integration

package testutil

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/require"
)

const (
	_mailWait  = 15 * time.Second
	_inboxSize = 32
)

var _tokenLink = regexp.MustCompile(`[?&]token=([A-Za-z0-9_-]+)`)

// Mail is a message the test SMTP server received.
type Mail struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Token is the one-time token of the first link in the text part.
func (m *Mail) Token(t *testing.T) string {
	t.Helper()

	match := _tokenLink.FindStringSubmatch(m.Text)
	require.NotNil(t, match, m.Text)

	return match[1]
}

// SMTPServer is an SMTP server that keeps every message in memory.
type SMTPServer struct {
	URL   string
	inbox chan *Mail
}

// StartSMTP starts an SMTP server on a free local port for the test.
func StartSMTP(t *testing.T) *SMTPServer {
	t.Helper()

	s := &SMTPServer{URL: "", inbox: make(chan *Mail, _inboxSize)}

	srv := smtp.NewServer(&backend{inbox: s.inbox})
	srv.AllowInsecureAuth = true
	srv.Domain = "localhost"

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	s.URL = "smtp://" + ln.Addr().String() + "?tls=none"

	return s
}

// Next waits for the next message to.
func (s *SMTPServer) Next(t *testing.T, to string) *Mail {
	t.Helper()

	timeout := time.After(_mailWait)

	for {
		select {
		case m := <-s.inbox:
			if strings.EqualFold(m.To, to) {
				return m
			}
		case <-timeout:
			t.Fatalf("testutil: no mail to %s", to)

			return nil
		}
	}
}

type backend struct {
	inbox chan *Mail
}

func (b *backend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &session{inbox: b.inbox, to: ""}, nil
}

type session struct {
	inbox chan *Mail
	to    string
}

func (s *session) Mail(string, *smtp.MailOptions) error { return nil }

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.to = to

	return nil
}

func (s *session) Data(r io.Reader) error {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return err //nolint:wrapcheck // reported to the SMTP client
	}

	dec := new(mime.WordDecoder)

	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		return err //nolint:wrapcheck // reported to the SMTP client
	}

	m := &Mail{To: s.to, Subject: subject, Text: "", HTML: ""}

	if err := readParts(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, m); err != nil {
		return err
	}

	s.inbox <- m

	return nil
}

func readParts(contentType, encoding string, body io.Reader, m *Mail) error {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return err //nolint:wrapcheck // reported to the SMTP client
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		return readMultipart(multipart.NewReader(body, params["boundary"]), m)
	}

	switch strings.ToLower(encoding) {
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, body)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, body); err != nil {
		return err //nolint:wrapcheck // reported to the SMTP client
	}

	switch mediaType {
	case "text/plain":
		m.Text = buf.String()
	case "text/html":
		m.HTML = buf.String()
	}

	return nil
}

func readMultipart(mr *multipart.Reader, m *Mail) error {
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err //nolint:wrapcheck // reported to the SMTP client
		}

		if err := readParts(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, m); err != nil {
			return err
		}
	}
}

func (s *session) Reset() {}

func (s *session) Logout() error { return nil }
