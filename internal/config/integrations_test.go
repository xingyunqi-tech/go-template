package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

func TestIntegrationDefaults(t *testing.T) {
	setSecrets(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 300, cfg.SMS.CodeExpires)
	assert.Equal(t, 6, cfg.SMS.CodeLength)
	assert.Equal(t, 5, cfg.SMS.MaxAttempts)
	assert.Equal(t, 60, cfg.SMS.ResendInterval)
	assert.Equal(t, "dysmsapi.aliyuncs.com", cfg.SMS.Endpoint)
	assert.False(t, cfg.SMS.MockMode.Or(false))
	assert.Equal(t, "RSA2", cfg.Alipay.SignType)
	assert.Equal(t, "utf-8", cfg.Alipay.Charset)
	assert.Equal(t, config.AlipayProductionGateway, cfg.Alipay.Gateway())

	t.Setenv("ALIPAY_SANDBOX", "true")
	t.Setenv("SMS_MOCK_MODE", "true")
	t.Setenv("WECHAT_MINI_APP_ID", "wx1")

	cfg, err = config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.AlipaySandboxGateway, cfg.Alipay.Gateway())
	assert.True(t, cfg.SMS.MockMode.Or(false))
	assert.Equal(t, "wx1", cfg.Wechat.MiniAppID)

	t.Setenv("ALIPAY_GATEWAY", "https://gateway.example.com")

	cfg, err = config.Load()
	require.NoError(t, err)
	assert.Equal(t, "https://gateway.example.com", cfg.Alipay.Gateway())
}

func TestIntegrationRangesAreChecked(t *testing.T) {
	setSecrets(t)
	t.Setenv("SMS_CODE_LENGTH", "9")
	t.Setenv("SMS_CODE_EXPIRES", "30")
	t.Setenv("SMS_MAX_ATTEMPTS", "0")
	t.Setenv("SMS_RESEND_INTERVAL", "301")
	t.Setenv("ALIPAY_SIGN_TYPE", "SM2")

	_, err := config.Load()
	require.Error(t, err)

	for _, name := range []string{"SMS_CODE_LENGTH", "SMS_CODE_EXPIRES", "SMS_MAX_ATTEMPTS", "SMS_RESEND_INTERVAL", "ALIPAY_SIGN_TYPE"} {
		assert.Contains(t, err.Error(), name)
	}
}

func TestWechatSubscribeDefaults(t *testing.T) {
	setSecrets(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "formal", cfg.Wechat.MiniProgramState)
	assert.Equal(t, map[string]config.SubscribeTemplate{"orderPaid": {TemplateID: "", Fields: nil}}, cfg.Wechat.SubscribeTemplates())

	t.Setenv("WECHAT_MINI_PROGRAM_STATE", "")
	t.Setenv("WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS", "")

	cfg, err = config.Load()
	require.NoError(t, err)
	assert.Equal(t, "formal", cfg.Wechat.MiniProgramState)
	assert.Empty(t, cfg.Wechat.SubscribeTemplates()["orderPaid"].Fields)
}

func TestWechatSubscribeTemplateParsing(t *testing.T) {
	setSecrets(t)
	t.Setenv("WECHAT_MINI_PROGRAM_STATE", "developer")
	t.Setenv("WECHAT_SUBSCRIBE_ORDER_PAID_TEMPLATE_ID", "TPL1")
	t.Setenv("WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS", " orderNo : character_string1,amount:amount2 ,paidAt:time3")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "developer", cfg.Wechat.MiniProgramState)
	assert.Equal(t, map[string]config.SubscribeTemplate{"orderPaid": {TemplateID: "TPL1", Fields: map[string]string{
		"orderNo": "character_string1", "amount": "amount2", "paidAt": "time3",
	}}}, cfg.Wechat.SubscribeTemplates())
}

func TestWechatSubscribeSettingsAreChecked(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS", "orderNo=character_string1"},
		{"WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS", "orderNo:character_string1,"},
		{"WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS", "order-no:thing1"},
		{"WECHAT_MINI_PROGRAM_STATE", "release"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			setSecrets(t)
			t.Setenv(tc.name, tc.value)

			_, err := config.Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.name)
		})
	}
}
