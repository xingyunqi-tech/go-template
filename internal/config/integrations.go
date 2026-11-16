package config

import (
	"errors"
	"regexp"
	"strings"
)

// Alipay gateways chosen by ALIPAY_SANDBOX when ALIPAY_GATEWAY is unset.
const (
	AlipayProductionGateway = "https://openapi.alipay.com/gateway.do"
	AlipaySandboxGateway    = "https://openapi.alipaydev.com/gateway.do"
)

// SMS configures Aliyun Dysms and the verification code rules like nestjs sms.config; times are
// in seconds.
type SMS struct {
	AccessKeyID     string       `env:"SMS_ACCESS_KEY_ID"`
	AccessKeySecret Secret       `env:"SMS_ACCESS_KEY_SECRET"`
	SignName        string       `env:"SMS_SIGN_NAME"`
	TemplateCode    string       `env:"SMS_TEMPLATE_CODE"`
	CodeExpires     int          `env:"SMS_CODE_EXPIRES"      envDefault:"300"                   validate:"min=60,max=600"`
	CodeLength      int          `env:"SMS_CODE_LENGTH"       envDefault:"6"                     validate:"min=4,max=8"`
	Endpoint        string       `env:"SMS_ENDPOINT"          envDefault:"dysmsapi.aliyuncs.com" validate:"required"`
	MockMode        OptionalBool `env:"SMS_MOCK_MODE"`
	MaxAttempts     int          `env:"SMS_MAX_ATTEMPTS"      envDefault:"5"                     validate:"min=1,max=10"`
	ResendInterval  int          `env:"SMS_RESEND_INTERVAL"   envDefault:"60"                    validate:"min=30,max=300"`
}

// Wechat holds the mini program and open platform (app) credentials used for sign-in and the
// health probe, and the subscribe message settings.
type Wechat struct {
	MiniAppID               string          `env:"WECHAT_MINI_APP_ID"`
	MiniAppSecret           Secret          `env:"WECHAT_MINI_APP_SECRET"`
	AppID                   string          `env:"WECHAT_APP_ID"`
	AppSecret               Secret          `env:"WECHAT_APP_SECRET"`
	MiniProgramState        string          `env:"WECHAT_MINI_PROGRAM_STATE"               envDefault:"formal" validate:"oneof=developer trial formal"`
	OrderPaidTemplateID     string          `env:"WECHAT_SUBSCRIBE_ORDER_PAID_TEMPLATE_ID"`
	OrderPaidTemplateFields SubscribeFields `env:"WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS"`
}

// SubscribeTemplate is a subscribe message template: its ID and the template field key of each
// business field.
type SubscribeTemplate struct {
	TemplateID string
	Fields     map[string]string
}

// SubscribeTemplates returns the subscribe message templates by scene.
func (w *Wechat) SubscribeTemplates() map[string]SubscribeTemplate {
	return map[string]SubscribeTemplate{
		"orderPaid": {TemplateID: w.OrderPaidTemplateID, Fields: w.OrderPaidTemplateFields},
	}
}

var (
	_subscribeFieldsPattern = regexp.MustCompile(`^\s*\w+\s*:\s*\w+\s*(,\s*\w+\s*:\s*\w+\s*)*$`)

	errSubscribeFields = errors.New("WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS must be like orderNo:character_string1,amount:amount2")
)

// SubscribeFields maps business fields to template field keys, written as
// "orderNo:character_string1,amount:amount2".
type SubscribeFields map[string]string

// UnmarshalText implements encoding.TextUnmarshaler.
func (f *SubscribeFields) UnmarshalText(text []byte) error {
	value := string(text)
	if !_subscribeFieldsPattern.MatchString(value) {
		return errSubscribeFields
	}

	fields := SubscribeFields{}

	for pair := range strings.SplitSeq(value, ",") {
		name, key, _ := strings.Cut(pair, ":")
		fields[strings.TrimSpace(name)] = strings.TrimSpace(key)
	}

	*f = fields

	return nil
}

// Alipay holds what alipay.system.oauth.token needs.
type Alipay struct {
	AppID      string       `env:"ALIPAY_APP_ID"`
	PrivateKey Secret       `env:"ALIPAY_PRIVATE_KEY"`
	GatewayURL string       `env:"ALIPAY_GATEWAY"`
	SignType   string       `env:"ALIPAY_SIGN_TYPE"   envDefault:"RSA2"  validate:"oneof=RSA2 RSA"`
	Charset    string       `env:"ALIPAY_CHARSET"     envDefault:"utf-8" validate:"required"`
	Sandbox    OptionalBool `env:"ALIPAY_SANDBOX"`
}

// Gateway returns ALIPAY_GATEWAY, or the sandbox or production gateway.
func (a *Alipay) Gateway() string {
	switch {
	case a.GatewayURL != "":
		return a.GatewayURL
	case a.Sandbox.Or(false):
		return AlipaySandboxGateway
	default:
		return AlipayProductionGateway
	}
}
