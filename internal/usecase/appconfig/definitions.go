package appconfig

// Definition is a registered config entry (nestjs CONFIG_DEFINITIONS); its value is stored as
// {"value": <value>}.
type Definition struct {
	Key         string
	Type        string
	IsPublic    bool
	Description string
	// Default is used while the entry is not stored; nil means no default.
	Default any
	// Dedicated entries are written only through their own endpoint.
	Dedicated bool
}

// Value types.
const (
	TypeBoolean = "boolean"
	TypeNumber  = "number"
	TypeString  = "string"
	TypeJSON    = "json"
)

// Registered keys read by other modules.
const (
	KeyPrivacyPolicyVersion = "consent.privacyPolicyVersion"
	KeyUserAgreementVersion = "consent.userAgreementVersion"
	KeyAIServiceVersion     = "consent.aiServiceVersion"
	KeyAppProfile           = "app.profile"
)

// Definitions lists the registered entries in nestjs order.
//
//nolint:mnd // registered default values
func Definitions() []Definition {
	return []Definition{
		{Key: "ai.enabled", Type: TypeBoolean, IsPublic: true, Description: "AI 功能总开关", Default: true},
		{Key: "ai.dailyBudget", Type: TypeNumber, IsPublic: false, Description: "全站每日成本上限（元），超过即熔断"},
		{Key: "ai.quota.chat", Type: TypeNumber, IsPublic: true, Description: "用户每日讲题/对话次数", Default: 20},
		{Key: "ai.quota.grading", Type: TypeNumber, IsPublic: true, Description: "用户每日批改次数", Default: 3},
		{Key: "ai.ratePerMinute", Type: TypeNumber, IsPublic: false, Description: "每用户每分钟 AI 请求上限", Default: 10},
		{Key: "ai.paperGradingLimit", Type: TypeNumber, IsPublic: false, Description: "每份试卷自动批改主观题上限", Default: 5},
		{Key: "ai.model", Type: TypeString, IsPublic: false, Description: "当前使用的模型标识"},
		{Key: "wrongBook.masterThreshold", Type: TypeNumber, IsPublic: true, Description: "错题连续答对阈值", Default: 3},
		{Key: "practice.offlineSyncWindowMin", Type: TypeNumber, IsPublic: false, Description: "模考离线补交窗口（分钟）", Default: 5},
		{Key: "practice.dailySetSize", Type: TypeNumber, IsPublic: false, Description: "每日一练题量", Default: 10},
		{Key: "practice.trialSize", Type: TypeNumber, IsPublic: true, Description: "游客试做题数", Default: 3},
		{Key: "paper.generateDailyLimit", Type: TypeNumber, IsPublic: true, Description: "每用户每日按规则生成试卷次数", Default: 5},
		{Key: KeyPrivacyPolicyVersion, Type: TypeString, IsPublic: true, Description: "隐私政策当前版本"},
		{Key: KeyUserAgreementVersion, Type: TypeString, IsPublic: true, Description: "用户协议当前版本"},
		{Key: KeyAIServiceVersion, Type: TypeString, IsPublic: true, Description: "AI 服务声明当前版本"},
		{Key: "agreement.userAgreement", Type: TypeString, IsPublic: true, Description: "用户协议正文（HTML），未配置时小程序显示内置文本"},
		{Key: "agreement.privacyPolicy", Type: TypeString, IsPublic: true, Description: "隐私政策正文（HTML），未配置时小程序显示内置文本"},
		{Key: "agreement.aiService", Type: TypeString, IsPublic: true, Description: "AI 服务声明正文（HTML），未配置时小程序显示内置文本"},
		{
			Key: KeyAppProfile, Type: TypeJSON, IsPublic: false, Dedicated: true,
			Description: "小程序站点信息（产品名称、主体、客服等），未配置的字段由小程序使用内置值",
		},
		{Key: "alert.thresholds", Type: TypeJSON, IsPublic: false, Description: "各监控项告警阈值"},
	}
}

// Find returns the definition of key.
func Find(key string) (Definition, bool) {
	for _, d := range Definitions() {
		if d.Key == key {
			return d, true
		}
	}

	return Definition{}, false
}
