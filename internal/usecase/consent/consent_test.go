package consent_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/internal/usecase/consent"
)

type configs map[string]string

func (c configs) Get(_ context.Context, key string) (*entity.Config, error) {
	v, ok := c[key]
	if !ok {
		return nil, nil //nolint:nilnil // absent entry
	}

	return &entity.Config{Key: key, Value: []byte(v), IsPublic: true, CreatedAt: time.Time{}, UpdatedAt: time.Time{}}, nil
}

func (c configs) Upsert(context.Context, string, []byte, *bool) (*entity.Config, error) {
	panic("not used")
}

type now struct{}

func (now) AfterCommit(ctx context.Context, fn func(context.Context)) { fn(ctx) }

type fakeConsents struct {
	rows []entity.UserConsent
}

func (f *fakeConsents) InsertIfAbsent(ctx context.Context, c *entity.UserConsent) error {
	if found, _ := f.Get(ctx, c.UserID, c.Type, c.Version); found == nil {
		c.AgreedAt = time.Date(2026, 11, 1, 0, 0, len(f.rows), 0, time.UTC)
		f.rows = append(f.rows, *c)
	}

	return nil
}

func (f *fakeConsents) Latest(_ context.Context, userID int) ([]entity.UserConsent, error) {
	latest := map[string]entity.UserConsent{}

	for _, r := range f.rows {
		if r.UserID == userID && r.AgreedAt.After(latest[r.Type].AgreedAt) {
			latest[r.Type] = r
		}
	}

	out := make([]entity.UserConsent, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}

	return out, nil
}

func (f *fakeConsents) Get(_ context.Context, userID int, consentType, version string) (*entity.UserConsent, error) {
	for i := range f.rows {
		if r := f.rows[i]; r.UserID == userID && r.Type == consentType && r.Version == version {
			return &f.rows[i], nil
		}
	}

	return nil, nil //nolint:nilnil // not agreed
}

func (f *fakeConsents) Exists(ctx context.Context, userID int, consentType, version string) (bool, error) {
	c, err := f.Get(ctx, userID, consentType, version)

	return c != nil, err
}

func newService(versions configs, rows ...entity.UserConsent) (*consent.Service, *fakeConsents) {
	repo := &fakeConsents{rows: rows}
	config := appconfig.New(versions, redis.NewMemory(), now{}, slog.New(slog.DiscardHandler))

	return consent.New(repo, config), repo
}

func strPtr(s string) *string {
	return &s
}

func TestStatusesFollowCurrentVersions(t *testing.T) {
	t.Parallel()

	agreedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newService(
		configs{
			appconfig.KeyPrivacyPolicyVersion: `{"value": "v2"}`,
			appconfig.KeyUserAgreementVersion: `{"value": " "}`,
			appconfig.KeyAIServiceVersion:     `{"value": 3}`,
		},
		entity.UserConsent{UserID: 7, Type: entity.ConsentPrivacyPolicy, Version: "v1", AgreedAt: agreedAt},
		entity.UserConsent{UserID: 7, Type: entity.ConsentUserAgreement, Version: "u1", AgreedAt: agreedAt},
	)

	statuses, err := svc.Statuses(t.Context(), 7)
	require.NoError(t, err)

	assert.Equal(t, []consent.Status{
		{Type: entity.ConsentPrivacyPolicy, CurrentVersion: strPtr("v2"), AgreedVersion: strPtr("v1"), AgreedAt: &agreedAt, Agreed: false, NeedsConsent: true},
		{Type: entity.ConsentUserAgreement, CurrentVersion: nil, AgreedVersion: strPtr("u1"), AgreedAt: &agreedAt, Agreed: false, NeedsConsent: false},
		{Type: entity.ConsentAIService, CurrentVersion: nil, AgreedVersion: nil, AgreedAt: nil, Agreed: false, NeedsConsent: false},
	}, statuses)
}

func TestAgreedCurrentChecksOlderRecords(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newService(
		configs{appconfig.KeyPrivacyPolicyVersion: `{"value": "v1"}`},
		entity.UserConsent{UserID: 7, Type: entity.ConsentPrivacyPolicy, Version: "v1", AgreedAt: older},
		entity.UserConsent{UserID: 7, Type: entity.ConsentPrivacyPolicy, Version: "v2", AgreedAt: newer},
	)

	statuses, err := svc.Statuses(t.Context(), 7)
	require.NoError(t, err)
	assert.Equal(t, "v2", *statuses[0].AgreedVersion)
	assert.True(t, statuses[0].Agreed)

	agreed, err := svc.HasAgreedCurrent(t.Context(), 7, entity.ConsentPrivacyPolicy)
	require.NoError(t, err)
	assert.True(t, agreed)

	agreed, err = svc.HasAgreedCurrent(t.Context(), 7, entity.ConsentAIService)
	require.NoError(t, err)
	assert.False(t, agreed)
}

func TestAgreeKeepsFirstRecord(t *testing.T) {
	t.Parallel()

	svc, repo := newService(configs{appconfig.KeyAIServiceVersion: `{"value": "a1"}`})
	ctx := t.Context()

	_, err := svc.Agree(ctx, 7, entity.ConsentAIService, "a0", nil)

	var httpErr *entity.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, []entity.Detail{{Name: "version", Value: "consentVersionNotCurrent"}}, httpErr.Fields)

	_, err = svc.Agree(ctx, 7, entity.ConsentPrivacyPolicy, "a1", nil)
	require.ErrorAs(t, err, &httpErr)

	first, err := svc.Agree(ctx, 7, entity.ConsentAIService, "a1", strPtr("10.0.0.1"))
	require.NoError(t, err)

	second, err := svc.Agree(ctx, 7, entity.ConsentAIService, "a1", strPtr("10.0.0.2"))
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.True(t, second.Agreed)
	assert.False(t, second.NeedsConsent)
	require.Len(t, repo.rows, 1)
	assert.Equal(t, "10.0.0.1", *repo.rows[0].IP)
}
