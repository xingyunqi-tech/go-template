package config

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
// health probe.
type Wechat struct {
	MiniAppID     string `env:"WECHAT_MINI_APP_ID"`
	MiniAppSecret Secret `env:"WECHAT_MINI_APP_SECRET"`
	AppID         string `env:"WECHAT_APP_ID"`
	AppSecret     Secret `env:"WECHAT_APP_SECRET"`
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
