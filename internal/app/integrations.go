package app

import (
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
)

// outbound is the untraced transport of the SMS, WeChat and Alipay clients, whose request URLs
// carry secrets, codes and phone numbers.
func outbound(infra *Infra) http.RoundTripper {
	if infra.Outbound == nil {
		return http.DefaultTransport
	}

	return infra.Outbound
}

func newSMS(cfg *config.Config, log *slog.Logger, infra *Infra, cache repo.Cache) *sms.Service {
	c := &cfg.SMS
	provider := smsapi.New(&smsapi.Config{
		AccessKeyID: c.AccessKeyID, AccessKeySecret: c.AccessKeySecret.Value(), SignName: c.SignName,
		TemplateCode: c.TemplateCode, Endpoint: c.Endpoint, Mock: c.MockMode.Or(false),
	}, outbound(infra), log)

	return sms.NewService(cache, redis.NewLocks(infra.Redis, log), provider, sms.Config{
		CodeLength: c.CodeLength, CodeTTL: time.Duration(c.CodeExpires) * time.Second,
		MaxAttempts: c.MaxAttempts, ResendInterval: time.Duration(c.ResendInterval) * time.Second,
	}, log)
}

func newWechat(cfg *config.Config, log *slog.Logger, infra *Infra, cache repo.Cache) *wechat.Client {
	return wechat.New(&wechat.Config{
		MiniAppID: cfg.Wechat.MiniAppID, MiniAppSecret: cfg.Wechat.MiniAppSecret.Value(),
		AppID: cfg.Wechat.AppID, AppSecret: cfg.Wechat.AppSecret.Value(),
	}, cache, outbound(infra), log)
}

func newAlipay(cfg *config.Config, log *slog.Logger, infra *Infra) *alipay.Client {
	return alipay.New(&alipay.Config{
		AppID: cfg.Alipay.AppID, PrivateKey: cfg.Alipay.PrivateKey.Value(), Gateway: cfg.Alipay.Gateway(),
		SignType: cfg.Alipay.SignType, Charset: cfg.Alipay.Charset,
	}, outbound(infra), log)
}
