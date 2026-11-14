// Package consent records agreement consents (nestjs ConsentsService): current versions come from
// config entries, and agreeing to the same version again keeps the first record.
package consent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

func versionKeys() map[string]string {
	return map[string]string{
		entity.ConsentPrivacyPolicy: appconfig.KeyPrivacyPolicyVersion,
		entity.ConsentUserAgreement: appconfig.KeyUserAgreementVersion,
		entity.ConsentAIService:     appconfig.KeyAIServiceVersion,
	}
}

// Status is the consent state of one agreement.
type Status struct {
	Type           string
	CurrentVersion *string
	AgreedVersion  *string
	AgreedAt       *time.Time
	Agreed         bool
	NeedsConsent   bool
}

// Service reads and records consents.
type Service struct {
	consents repo.Consents
	config   *appconfig.Service
}

// New returns a Service.
func New(consents repo.Consents, config *appconfig.Service) *Service {
	return &Service{consents: consents, config: config}
}

func newStatus(consentType string, current *string, latest *entity.UserConsent, agreed bool) *Status {
	s := &Status{
		Type: consentType, CurrentVersion: current, AgreedVersion: nil, AgreedAt: nil,
		Agreed: agreed, NeedsConsent: current != nil && !agreed,
	}

	if latest != nil {
		s.AgreedVersion = &latest.Version
		s.AgreedAt = &latest.AgreedAt
	}

	return s
}

// Statuses returns the state of every agreement in ConsentType order.
func (s *Service) Statuses(ctx context.Context, userID int) ([]Status, error) {
	types := entity.ConsentTypes()
	versions := make([]*string, len(types))

	for i, t := range types {
		v, err := s.currentVersion(ctx, t)
		if err != nil {
			return nil, err
		}

		versions[i] = v
	}

	latest, err := s.consents.Latest(ctx, userID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	byType := make(map[string]*entity.UserConsent, len(latest))
	for i := range latest {
		byType[latest[i].Type] = &latest[i]
	}

	out := make([]Status, len(types))

	for i, t := range types {
		agreed, err := s.agreedCurrent(ctx, userID, t, versions[i], byType[t])
		if err != nil {
			return nil, err
		}

		out[i] = *newStatus(t, versions[i], byType[t], agreed)
	}

	return out, nil
}

// Agree records the consent; version must be the current one.
func (s *Service) Agree(ctx context.Context, userID int, consentType, version string, ip *string) (*Status, error) {
	current, err := s.currentVersion(ctx, consentType)
	if err != nil {
		return nil, err
	}

	if current == nil || version != *current {
		return nil, entity.Invalid("version", "consentVersionNotCurrent")
	}

	c := &entity.UserConsent{UserID: userID, Type: consentType, Version: version, IP: ip}
	if err := s.consents.InsertIfAbsent(ctx, c); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	agreed, err := s.consents.Get(ctx, userID, consentType, version)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return newStatus(consentType, current, agreed, true), nil
}

// HasAgreedCurrent reports whether the user agreed to the current version; false while no current
// version is configured.
func (s *Service) HasAgreedCurrent(ctx context.Context, userID int, consentType string) (bool, error) {
	current, err := s.currentVersion(ctx, consentType)
	if err != nil || current == nil {
		return false, err
	}

	return s.consents.Exists(ctx, userID, consentType, *current) //nolint:wrapcheck // repo errors are wrapped
}

func (s *Service) currentVersion(ctx context.Context, consentType string) (*string, error) {
	raw, ok, err := s.config.Value(ctx, versionKeys()[consentType])
	if err != nil || !ok {
		return nil, err //nolint:wrapcheck // config errors are wrapped
	}

	var v string
	if json.Unmarshal(raw, &v) != nil || validatorx.Trim(v) == "" {
		return nil, nil //nolint:nilnil // no current version
	}

	return &v, nil
}

func (s *Service) agreedCurrent(ctx context.Context, userID int, consentType string, current *string, latest *entity.UserConsent) (bool, error) {
	if current == nil || latest == nil {
		return false, nil
	}

	if latest.Version == *current {
		return true, nil
	}

	return s.consents.Exists(ctx, userID, consentType, *current) //nolint:wrapcheck // repo errors are wrapped
}
