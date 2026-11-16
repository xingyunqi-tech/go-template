package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/riverqueue/river"
	"golang.org/x/time/rate"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
)

const (
	_notificationRate = 10

	_smsNotInitialized = "SMS client not initialized"
	_noOpenID          = "User has no WeChat openId"
	_unprocessable     = "Unprocessable Entity Exception"
)

// ErrUnrecoverable marks a subscribe message failure that retries cannot fix: WeChat error codes
// 43101, 40003, 40037, 41030 and 47003, or a mini program without AppID / Secret.
var ErrUnrecoverable = errors.New("unrecoverable")

type unrecoverableError struct {
	err error
}

func (e *unrecoverableError) Error() string { return e.err.Error() }

func (e *unrecoverableError) Unwrap() []error { return []error{ErrUnrecoverable, e.err} }

// Unrecoverable marks err as ErrUnrecoverable and keeps its message.
func Unrecoverable(err error) error {
	return &unrecoverableError{err: err}
}

// failureMessage is the nestjs error.message of a delivery failure.
func failureMessage(err error) string {
	var httpErr *entity.Error
	if errors.As(err, &httpErr) && httpErr.Message == "" && httpErr.Status == entity.StatusUnprocessable {
		return _unprocessable
	}

	return err.Error()
}

// failure is a delivery error with the nestjs message.
type failure string

func (f failure) Error() string { return string(f) }

// SMSSender sends a templated SMS; the error message is the recorded failure.
type SMSSender interface {
	Send(ctx context.Context, phone string, params map[string]string) error
}

// SubscribeMessage is a WeChat mini program subscribe message.
type SubscribeMessage struct {
	OpenID     string
	TemplateID string
	Page       *string
	Data       map[string]string
}

// SubscribeSender sends WeChat subscribe messages.
type SubscribeSender interface {
	SendSubscribeMessage(ctx context.Context, m *SubscribeMessage) error
}

// SubscribeTemplate maps the business fields of a scene to the template field keys.
type SubscribeTemplate struct {
	TemplateID string
	Fields     map[string]string
}

// NotificationDeps are what the notification queue workers use; a nil SMS or Subscribe sender
// is an unconfigured integration.
type NotificationDeps struct {
	Notifications *notification.Service
	Users         repo.Users
	SMS           SMSSender
	Subscribe     SubscribeSender
	Templates     map[string]SubscribeTemplate
}

// notifier delivers the external channels of notifications, at most 10 jobs a second.
type notifier struct {
	deps    NotificationDeps
	sender  mail.Sender
	limiter *rate.Limiter
	log     *slog.Logger
}

func newNotifier(deps *NotificationDeps, sender mail.Sender, log *slog.Logger) *notifier {
	return &notifier{deps: *deps, sender: sender, limiter: rate.NewLimiter(_notificationRate, _notificationRate), log: log}
}

func (n *notifier) user(ctx context.Context, id int) (*entity.User, error) {
	return n.deps.Users.Get(ctx, repo.UserQuery{ID: &id, Email: nil, Phone: nil, WechatOpenID: nil, AlipayOpenID: nil}) //nolint:wrapcheck // repo errors are wrapped
}

// record stores a channel result; a failure to store it is only logged.
func (n *notifier) record(ctx context.Context, id int, channel string, failure error) {
	message := ""
	if failure != nil {
		message = failureMessage(failure)
	}

	if err := n.deps.Notifications.RecordChannelResult(ctx, id, channel, failure == nil, message); err != nil {
		n.log.WarnContext(ctx, "failed to record notification channel result", "channel", channel, "notification_id", id, "error", err.Error())
	}
}

func blank(s *string) bool {
	return s == nil || *s == ""
}

func (n *notifier) email(ctx context.Context, job *entity.NotificationEmailJob) error {
	if err := n.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("worker: rate limit: %w", err)
	}

	u, err := n.user(ctx, job.UserID)
	if err == nil && (u == nil || blank(u.Email)) {
		err = failure(fmt.Sprintf("User %d not found or has no email", job.UserID))
	}

	if err == nil {
		err = n.sender.Send(ctx, &mail.Outgoing{To: *u.Email, Subject: job.Title, Text: job.Content, HTML: "<p>" + job.Content + "</p>"})
	}

	n.record(ctx, job.NotificationID, entity.ChannelEmail, err)

	if err != nil {
		return err
	}

	n.log.InfoContext(ctx, "notification email sent", "notification_id", job.NotificationID)

	return nil
}

func (n *notifier) sms(ctx context.Context, job *entity.NotificationSMSJob) error {
	if err := n.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("worker: rate limit: %w", err)
	}

	u, err := n.user(ctx, job.UserID)

	switch {
	case err != nil:
	case u == nil || blank(u.Phone):
		err = failure(fmt.Sprintf("User %d not found or has no phone", job.UserID))
	case n.deps.SMS == nil:
		err = failure(_smsNotInitialized)
	default:
		err = n.deps.SMS.Send(ctx, *u.Phone, map[string]string{"content": job.Content})
	}

	n.record(ctx, job.NotificationID, entity.ChannelSMS, err)

	if err != nil {
		return err
	}

	n.log.InfoContext(ctx, "notification sms sent", "notification_id", job.NotificationID)

	return nil
}

type subscribePayload struct {
	Scene  string         `json:"scene"`
	Values map[string]any `json:"values"`
	Page   *string        `json:"page"`
}

type fieldLimit struct {
	prefix string
	max    int
}

// subscribeLimits are the value length limits of the template field types, matched by key prefix.
func subscribeLimits() []fieldLimit {
	return []fieldLimit{
		{"character_string", 32},
		{"car_number", 8},
		{"thing", 20},
		{"number", 32},
		{"letter", 32},
		{"symbol", 5},
		{"phrase", 5},
		{"name", 10},
	}
}

// FormatSubscribeValue truncates value to the length limit of the template field key.
func FormatSubscribeValue(key, value string) string {
	for _, rule := range subscribeLimits() {
		if strings.HasPrefix(key, rule.prefix) {
			if runes := []rune(value); len(runes) > rule.max {
				return string(runes[:rule.max])
			}

			return value
		}
	}

	return value
}

// BuildSubscribeMessage maps the payload values to the template fields; ok is false when the
// scene has no template.
func BuildSubscribeMessage(payload json.RawMessage, templates map[string]SubscribeTemplate) (msg *SubscribeMessage, scene string, ok bool) {
	var p subscribePayload

	_ = json.Unmarshal(payload, &p)

	t, found := templates[p.Scene]
	if !found || t.TemplateID == "" {
		return nil, p.Scene, false
	}

	data := map[string]string{}

	for name, key := range t.Fields {
		if v, has := p.Values[name]; has && v != nil {
			s, isString := v.(string)
			if !isString {
				s = fmt.Sprint(v)
			}

			data[key] = FormatSubscribeValue(key, s)
		}
	}

	return &SubscribeMessage{OpenID: "", TemplateID: t.TemplateID, Page: p.Page, Data: data}, p.Scene, true
}

func (n *notifier) wechatSubscribe(ctx context.Context, job *entity.NotificationWechatSubscribeJob) error {
	if err := n.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("worker: rate limit: %w", err)
	}

	msg, scene, ok := BuildSubscribeMessage(job.Payload, n.deps.Templates)
	if !ok {
		n.log.DebugContext(ctx, "subscribe template not configured, skipped", "scene", scene)

		return nil
	}

	u, err := n.user(ctx, job.UserID)
	if err != nil {
		return err
	}

	if u == nil || blank(u.WechatOpenID) {
		n.record(ctx, job.NotificationID, entity.ChannelPush, failure(_noOpenID))

		return nil
	}

	msg.OpenID = *u.WechatOpenID

	if n.deps.Subscribe == nil {
		err = Unrecoverable(failure(_unprocessable))
	} else {
		err = n.deps.Subscribe.SendSubscribeMessage(ctx, msg)
	}

	if err != nil {
		n.record(ctx, job.NotificationID, entity.ChannelPush, err)

		if errors.Is(err, ErrUnrecoverable) {
			return river.JobCancel(err) //nolint:wrapcheck // cancels the job
		}

		return err
	}

	n.record(ctx, job.NotificationID, entity.ChannelPush, nil)
	n.log.InfoContext(ctx, "wechat subscribe message sent", "notification_id", job.NotificationID)

	return nil
}

// NotificationEmailWorker sends notifications by mail.
type NotificationEmailWorker struct {
	river.WorkerDefaults[entity.NotificationEmailJob]

	notifier *notifier
}

// Work implements river.Worker.
func (w *NotificationEmailWorker) Work(ctx context.Context, job *river.Job[entity.NotificationEmailJob]) error {
	return w.notifier.email(ctx, &job.Args)
}

// NotificationSMSWorker sends notifications by SMS.
type NotificationSMSWorker struct {
	river.WorkerDefaults[entity.NotificationSMSJob]

	notifier *notifier
}

// Work implements river.Worker.
func (w *NotificationSMSWorker) Work(ctx context.Context, job *river.Job[entity.NotificationSMSJob]) error {
	return w.notifier.sms(ctx, &job.Args)
}

// NotificationWechatSubscribeWorker sends WeChat subscribe messages.
type NotificationWechatSubscribeWorker struct {
	river.WorkerDefaults[entity.NotificationWechatSubscribeJob]

	notifier *notifier
}

// Work implements river.Worker.
func (w *NotificationWechatSubscribeWorker) Work(ctx context.Context, job *river.Job[entity.NotificationWechatSubscribeJob]) error {
	return w.notifier.wechatSubscribe(ctx, &job.Args)
}

func addNotificationWorkers(workers *river.Workers, d *Deps) {
	n := newNotifier(&d.Notify, d.Sender, d.Log)

	river.AddWorker(workers, &NotificationEmailWorker{WorkerDefaults: river.WorkerDefaults[entity.NotificationEmailJob]{}, notifier: n})
	river.AddWorker(workers, &NotificationSMSWorker{WorkerDefaults: river.WorkerDefaults[entity.NotificationSMSJob]{}, notifier: n})
	river.AddWorker(workers, &NotificationWechatSubscribeWorker{
		WorkerDefaults: river.WorkerDefaults[entity.NotificationWechatSubscribeJob]{}, notifier: n,
	})
}
