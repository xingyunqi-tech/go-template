package entity

// Sign-in providers (platform conventions §11.1).
const (
	ProviderWechatMiniApp = "wechat_miniapp"
	ProviderWechatApp     = "wechat_app"
	ProviderAlipay        = "alipay"
)

// WechatProviders share accounts through the unionid.
func WechatProviders() []string {
	return []string{ProviderWechatMiniApp, ProviderWechatApp}
}

// ExternalIdentity is the account a provider vouches for: Subject is the openid or the Alipay
// user id, UnionID the WeChat unionid when known.
type ExternalIdentity struct {
	Provider string
	Subject  string
	UnionID  string
}

// SMS scenes (platform conventions §11.1).
const (
	SMSSceneLogin     = "login"
	SMSSceneBindPhone = "bind_phone"
)
