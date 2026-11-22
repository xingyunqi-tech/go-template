package entity

// Providers of users created by phone, WeChat and Alipay sign-in.
const (
	ProviderPhone  = "phone"
	ProviderWechat = "wechat"
	ProviderAlipay = "alipay"
)

const (
	_maskedPrefix   = 3
	_maskedSuffix   = 4
	_maskableLength = 7
)

// WechatUser is the identity WeChat returns for a sign-in code.
type WechatUser struct {
	OpenID   string
	UnionID  *string
	Nickname *string
	Gender   *int
}

// WechatPhone is the phone number bound to a WeChat account.
type WechatPhone struct {
	Number      string
	CountryCode string
}

// MaskPhone keeps the first 3 and last 4 characters of a phone number for logs.
func MaskPhone(phone string) string {
	switch {
	case phone == "":
		return "[invalid]"
	case len(phone) < _maskableLength:
		return "***"
	default:
		return phone[:_maskedPrefix] + "****" + phone[len(phone)-_maskedSuffix:]
	}
}

// NestUnprocessable is a 422 with an "error" key and errors, like nestjs
// UnprocessableEntityException({status, error, errors}).
func NestUnprocessable(key string, fields ...Detail) *NestError {
	return &NestError{Status: StatusUnprocessable, Message: "", ErrorKey: key, Fields: fields}
}
