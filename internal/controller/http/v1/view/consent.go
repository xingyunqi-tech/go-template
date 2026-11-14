package view

import (
	"github.com/xingyunqi-tech/go-template/internal/usecase/consent"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

// ConsentStatus is {type, currentVersion, agreedVersion, agreedAt, agreed, needsConsent}.
type ConsentStatus struct {
	Type           string         `json:"type"`
	CurrentVersion *string        `json:"currentVersion"`
	AgreedVersion  *string        `json:"agreedVersion"`
	AgreedAt       *jsontime.Time `json:"agreedAt"`
	Agreed         bool           `json:"agreed"`
	NeedsConsent   bool           `json:"needsConsent"`
}

// NewConsentStatus returns the view of s.
func NewConsentStatus(s *consent.Status) ConsentStatus {
	return ConsentStatus{
		Type: s.Type, CurrentVersion: s.CurrentVersion, AgreedVersion: s.AgreedVersion, AgreedAt: timePtr(s.AgreedAt),
		Agreed: s.Agreed, NeedsConsent: s.NeedsConsent,
	}
}

// NewConsentStatuses returns the views of statuses.
func NewConsentStatuses(statuses []consent.Status) []ConsentStatus {
	out := make([]ConsentStatus, len(statuses))
	for i := range statuses {
		out[i] = NewConsentStatus(&statuses[i])
	}

	return out
}
