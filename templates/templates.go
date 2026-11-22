// Package templates renders the mail templates with the texts of the request language.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
)

//go:embed mail/*.tmpl
var _mail embed.FS

// Mail renders the mails of the auth use case.
type Mail struct {
	html       *htmltemplate.Template
	text       *texttemplate.Template
	translator *i18n.Translator
}

var _ auth.MailRenderer = (*Mail)(nil)

// NewMail parses the embedded mail templates.
func NewMail(translator *i18n.Translator) (*Mail, error) {
	html, err := htmltemplate.ParseFS(_mail, "mail/*.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}

	text, err := texttemplate.ParseFS(_mail, "mail/*.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}

	return &Mail{html: html, text: text, translator: translator}, nil
}

type view struct {
	Lang     string
	Subject  string
	Greeting string
	Body     string
	Action   string
	Link     string
	Expires  string
	Ignore   string
}

func (m *Mail) message(lang, id string, data map[string]any) string {
	msg, _ := m.translator.Message(lang, id, data)

	return msg
}

// Render implements auth.MailRenderer.
func (m *Mail) Render(kind, locale string, data *auth.MailData) (*repo.Mail, error) {
	v := view{
		Lang: locale, Subject: m.message(locale, "mail."+kind+".subject", nil),
		Greeting: m.message(locale, "mail.greetingAnonymous", nil), Body: m.message(locale, "mail."+kind+".body", nil),
		Action: m.message(locale, "mail."+kind+".action", nil), Link: data.Link,
		Expires: m.message(locale, "mail."+kind+".expires", nil), Ignore: m.message(locale, "mail.ignore", nil),
	}

	if data.DisplayName != "" {
		v.Greeting = m.message(locale, "mail.greeting", map[string]any{"name": data.DisplayName})
	}

	var html, text bytes.Buffer

	if err := m.html.ExecuteTemplate(&html, kind+".html.tmpl", v); err != nil {
		return nil, fmt.Errorf("templates: %s: %w", kind, err)
	}

	if err := m.text.ExecuteTemplate(&text, kind+".txt.tmpl", v); err != nil {
		return nil, fmt.Errorf("templates: %s: %w", kind, err)
	}

	return &repo.Mail{To: "", Subject: v.Subject, Text: text.String(), HTML: html.String()}, nil
}
