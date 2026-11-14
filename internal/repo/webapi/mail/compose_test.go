package mail_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
)

const _hash = "eyJhbGciOi.pay-load_x"

func composer(t *testing.T, appName, frontend string) *mail.Composer {
	t.Helper()

	translator, err := i18n.New("en")
	require.NoError(t, err)

	c, err := mail.NewComposer(translator, appName, frontend)
	require.NoError(t, err)

	return c
}

func TestComposeMatchesNestjsRendering(t *testing.T) {
	t.Parallel()

	cases := []struct {
		template, lang, frontend string
		data                     map[string]any
	}{
		{"activation", "en", "http://localhost:3000, https://b.example", map[string]any{"hash": _hash}},
		{"reset-password", "zh", "https://app.example.com", map[string]any{"hash": _hash, "tokenExpires": int64(1762828800000)}},
		{"confirm-new-email", "zh", "https://app.example.com", map[string]any{"hash": _hash}},
	}

	for _, tc := range cases {
		t.Run(tc.template+"."+tc.lang, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile("../../../../tests/fixtures/nestjs_mail/" + tc.template + "." + tc.lang + ".json")
			require.NoError(t, err)

			var want struct{ Subject, Text, HTML string }
			require.NoError(t, json.Unmarshal(raw, &want))

			got, err := composer(t, "Tom's <App> & Co", tc.frontend).Compose(&mail.Message{
				Template: tc.template, To: "a@b.c", Data: tc.data, Language: tc.lang,
			})
			require.NoError(t, err)

			assert.Equal(t, want.Subject, got.Subject)
			assert.Equal(t, want.Text, got.Text)
			assert.Equal(t, want.HTML, got.HTML)
		})
	}
}

func TestUnknownLanguageFallsBack(t *testing.T) {
	t.Parallel()

	got, err := composer(t, "app", "http://x").Compose(&mail.Message{
		Template: "activation", To: "a@b.c", Data: map[string]any{"hash": "h"}, Language: "",
	})
	require.NoError(t, err)

	assert.Equal(t, "Confirm email", got.Subject)
	assert.Equal(t, "http://x/confirm-email?hash=h Confirm email", got.Text)
}

func TestLinkParamsUseFormEncoding(t *testing.T) {
	t.Parallel()

	got, err := composer(t, "app", "http://x").Compose(&mail.Message{
		Template: "activation", To: "a@b.c", Data: map[string]any{"hash": "a b~c+d"}, Language: "en",
	})
	require.NoError(t, err)

	assert.Equal(t, "http://x/confirm-email?hash=a+b%7Ec%2Bd Confirm email", got.Text)
}

func TestComposeNeedsFrontendDomainAndKnownTemplate(t *testing.T) {
	t.Parallel()

	_, err := composer(t, "app", "").Compose(&mail.Message{Template: "activation", To: "a", Data: map[string]any{"hash": "h"}, Language: "en"})
	require.Error(t, err)

	_, err = composer(t, "app", "http://x").Compose(&mail.Message{Template: "nope", To: "a", Data: nil, Language: "en"})
	require.Error(t, err)
}

func TestHandlebarsEscape(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a&#x3D;b&amp;c&lt;d&gt;&#x27;&quot;&#x60;", mail.HandlebarsEscape("a=b&c<d>'\"`"))
}

func TestSMTPMessageHeaders(t *testing.T) {
	t.Parallel()

	s := mail.NewSMTP(&mail.SMTPConfig{
		Host: "smtp.example.com", Port: 587, User: "", Password: "", DefaultEmail: "noreply@example.com", DefaultName: "Api",
		Secure: false, IgnoreTLS: false, RequireTLS: false,
	})

	msg, err := s.Message(&mail.Outgoing{To: "a@b.c", Subject: "Hi\r\nthere", Text: "text", HTML: "<p>html</p>"})
	require.NoError(t, err)

	var buf bytes.Buffer

	_, err = msg.WriteTo(&buf)
	require.NoError(t, err)

	raw := buf.String()
	assert.Contains(t, raw, `From: "Api" <noreply@example.com>`)
	assert.Contains(t, raw, "To: <a@b.c>")
	assert.Contains(t, raw, "Subject: Hi there")
	assert.Contains(t, raw, "multipart/alternative")
	assert.Contains(t, raw, "<p>html</p>")
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"john.doe@example.com": "jo***@example.com",
		"ab@example.com":       "a***@example.com",
		"a@example.com":        "a***@example.com",
		"@example.com":         "[invalid]",
		"nope":                 "[invalid]",
	}

	for in, want := range cases {
		assert.Equal(t, want, mail.MaskEmail(in), in)
	}
}
