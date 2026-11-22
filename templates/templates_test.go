package templates_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/templates"
)

func TestMailsAreRenderedInTheRequestLanguage(t *testing.T) {
	t.Parallel()

	translator, err := i18n.New("zh")
	require.NoError(t, err)

	r, err := templates.NewMail(translator)
	require.NoError(t, err)

	link := "https://app.example.com/reset-password?token=abc&x=<b>"

	zh, err := r.Render("reset_password", "zh", &auth.MailData{DisplayName: "Ada", Link: link})
	require.NoError(t, err)
	assert.Equal(t, "重置密码", zh.Subject)
	assert.Contains(t, zh.Text, "Ada，你好：")
	assert.Contains(t, zh.Text, link)
	assert.Contains(t, zh.Text, "30 分钟")
	assert.Contains(t, zh.HTML, `lang="zh"`)
	assert.Contains(t, zh.HTML, "token=abc&amp;x=%3cb%3e")
	assert.NotContains(t, zh.HTML, "<b>")

	en, err := r.Render("verify_email", "en", &auth.MailData{DisplayName: "", Link: link})
	require.NoError(t, err)
	assert.Equal(t, "Verify your email", en.Subject)
	assert.Contains(t, en.Text, "Hi,")
	assert.Contains(t, en.Text, "24 hours")

	_, err = r.Render("unknown", "en", &auth.MailData{DisplayName: "", Link: link})
	require.Error(t, err)
}
