// Package mail renders the nestjs mail templates and sends them over SMTP.
package mail

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/templates"
)

var (
	_placeholder = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

	errUnknownTemplate = errors.New("mail: unknown template")
	errNoFrontend      = errors.New("mail: FRONTEND_DOMAIN is required to build mail links")
	errMissingValue    = errors.New("mail: template value missing")
)

// Message is a templated mail as the request side queues it.
type Message struct {
	Template string
	To       string
	Data     map[string]any
	Language string
}

// Outgoing is a rendered mail.
type Outgoing struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type spec struct {
	titleKey string
	urlPath  string
	textKeys []string
}

func specs() map[string]spec {
	return map[string]spec{
		"activation": {
			titleKey: "confirmEmail", urlPath: "/confirm-email",
			textKeys: []string{"confirm-email.text1", "confirm-email.text2", "confirm-email.text3"},
		},
		"reset-password": {
			titleKey: "resetPassword", urlPath: "/password-change",
			textKeys: []string{"reset-password.text1", "reset-password.text2", "reset-password.text3", "reset-password.text4"},
		},
		"confirm-new-email": {
			titleKey: "confirmEmail", urlPath: "/confirm-new-email",
			textKeys: []string{"confirm-new-email.text1", "confirm-new-email.text2", "confirm-new-email.text3"},
		},
	}
}

// Composer is nestjs MailService: title and texts in the mail language, the link on the first
// FRONTEND_DOMAIN.
type Composer struct {
	translator     *i18n.Translator
	appName        string
	frontendDomain string
	templates      map[string]string
}

// NewComposer loads the templates.
func NewComposer(translator *i18n.Translator, appName, frontendDomain string) (*Composer, error) {
	loaded := map[string]string{}

	for name := range specs() {
		raw, err := fs.ReadFile(templates.Mail, "mail/"+name+".hbs")
		if err != nil {
			return nil, fmt.Errorf("mail: template %s: %w", name, err)
		}

		loaded[name] = string(raw)
	}

	return &Composer{translator: translator, appName: appName, frontendDomain: frontendDomain, templates: loaded}, nil
}

// HandlebarsEscape is the escapeExpression of Handlebars {{x}}.
func HandlebarsEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;", "`", "&#x60;", "=", "&#x3D;",
	).Replace(s)
}

// FormEncode is the WHATWG application/x-www-form-urlencoded serializer of URLSearchParams.
func FormEncode(s string) string {
	var b strings.Builder

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}

	return b.String()
}

func (c *Composer) t(lang, key string) string {
	if v, ok := c.translator.Translate(lang, key); ok {
		return v
	}

	return key
}

func linkParams(m *Message) [][2]string {
	params := [][2]string{{"hash", fmt.Sprint(m.Data["hash"])}}
	if m.Template == "reset-password" {
		params = append(params, [2]string{"expires", fmt.Sprint(m.Data["tokenExpires"])})
	}

	return params
}

func (c *Composer) link(path string, params [][2]string) (string, error) {
	if c.frontendDomain == "" {
		return "", errNoFrontend
	}

	base, _, _ := strings.Cut(c.frontendDomain, ",")

	u, err := url.Parse(strings.TrimSpace(base) + path)
	if err != nil {
		return "", fmt.Errorf("mail: FRONTEND_DOMAIN: %w", err)
	}

	pairs := make([]string, len(params))
	for i, p := range params {
		pairs[i] = FormEncode(p[0]) + "=" + FormEncode(p[1])
	}

	u.RawQuery = strings.Join(pairs, "&")

	return u.String(), nil
}

// Compose renders m in m.Language.
func (c *Composer) Compose(m *Message) (*Outgoing, error) {
	sp, ok := specs()[m.Template]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errUnknownTemplate, m.Template)
	}

	title := c.t(m.Language, "common."+sp.titleKey)

	link, err := c.link(sp.urlPath, linkParams(m))
	if err != nil {
		return nil, err
	}

	values := map[string]string{"title": title, "url": link, "actionTitle": title, "app_name": c.appName}
	for i, key := range sp.textKeys {
		values[fmt.Sprintf("text%d", i+1)] = c.t(m.Language, key)
	}

	var missing error

	html := _placeholder.ReplaceAllStringFunc(c.templates[m.Template], func(ph string) string {
		name := ph[2 : len(ph)-2]

		v, found := values[name]
		if !found {
			missing = fmt.Errorf("%w: %s", errMissingValue, name)
		}

		return HandlebarsEscape(v)
	})

	if missing != nil {
		return nil, missing
	}

	return &Outgoing{To: m.To, Subject: title, Text: link + " " + title, HTML: html}, nil
}
