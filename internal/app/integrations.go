package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/alipay"
	smsapi "github.com/xingyunqi-tech/go-template/internal/repo/webapi/sms"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/wechat"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
	"github.com/xingyunqi-tech/go-template/internal/worker"
)

// outbound is the untraced transport of the SMS, WeChat and Alipay clients, whose request URLs
// carry secrets, codes and phone numbers.
func outbound(infra *Infra) http.RoundTripper {
	if infra.Outbound == nil {
		return http.DefaultTransport
	}

	return infra.Outbound
}

func newSMSProvider(cfg *config.Config, log *slog.Logger, infra *Infra) *smsapi.Aliyun {
	c := &cfg.SMS

	return smsapi.New(&smsapi.Config{
		AccessKeyID: c.AccessKeyID, AccessKeySecret: c.AccessKeySecret.Value(), SignName: c.SignName,
		TemplateCode: c.TemplateCode, Endpoint: c.Endpoint, Mock: c.MockMode.Or(false),
	}, outbound(infra), log)
}

func newSMS(cfg *config.Config, log *slog.Logger, infra *Infra, cache repo.Cache) *sms.Service {
	c := &cfg.SMS

	return sms.NewService(cache, redis.NewLocks(infra.Redis, log), newSMSProvider(cfg, log, infra), sms.Config{
		CodeLength: c.CodeLength, CodeTTL: time.Duration(c.CodeExpires) * time.Second,
		MaxAttempts: c.MaxAttempts, ResendInterval: time.Duration(c.ResendInterval) * time.Second,
	}, log)
}

func newWechat(cfg *config.Config, log *slog.Logger, infra *Infra, cache repo.Cache) *wechat.Client {
	return wechat.New(&wechat.Config{
		MiniAppID: cfg.Wechat.MiniAppID, MiniAppSecret: cfg.Wechat.MiniAppSecret.Value(),
		AppID: cfg.Wechat.AppID, AppSecret: cfg.Wechat.AppSecret.Value(), MiniProgramState: cfg.Wechat.MiniProgramState,
	}, cache, outbound(infra), log)
}

// subscribeSender sends worker subscribe messages through the WeChat client.
type subscribeSender struct {
	client *wechat.Client
}

func (s subscribeSender) SendSubscribeMessage(ctx context.Context, m *worker.SubscribeMessage) error {
	err := s.client.SendSubscribeMessage(ctx, &wechat.SubscribeMessage{OpenID: m.OpenID, TemplateID: m.TemplateID, Page: m.Page, Data: m.Data})
	if err != nil && wechat.SubscribeUnrecoverable(err) {
		return worker.Unrecoverable(err)
	}

	return err //nolint:wrapcheck // the message is the recorded failure
}

func subscribeTemplates(cfg *config.Wechat) map[string]worker.SubscribeTemplate {
	out := map[string]worker.SubscribeTemplate{}
	for scene, t := range cfg.SubscribeTemplates() {
		out[scene] = worker.SubscribeTemplate{TemplateID: t.TemplateID, Fields: t.Fields}
	}

	return out
}

func newAlipay(cfg *config.Config, log *slog.Logger, infra *Infra) *alipay.Client {
	return alipay.New(&alipay.Config{
		AppID: cfg.Alipay.AppID, PrivateKey: cfg.Alipay.PrivateKey.Value(), Gateway: cfg.Alipay.Gateway(),
		SignType: cfg.Alipay.SignType, Charset: cfg.Alipay.Charset,
	}, outbound(infra), log)
}
