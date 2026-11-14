package entity

import (
	"time"
	"uuid"
)

// Config is a config row; Value is the stored JSON object.
type Config struct {
	Key       string
	Value     []byte
	IsPublic  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Region is an administrative division: level 1 province, 2 city, 3 district.
type Region struct {
	ID         int
	Code       string
	Name       string
	Level      int
	ParentCode *string
	Sort       int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Consent types (nestjs ConsentType), in enum order.
const (
	ConsentPrivacyPolicy = "PRIVACY_POLICY"
	ConsentUserAgreement = "USER_AGREEMENT"
	ConsentAIService     = "AI_SERVICE"
)

// ConsentTypes lists the consent types in enum order.
func ConsentTypes() []string {
	return []string{ConsentPrivacyPolicy, ConsentUserAgreement, ConsentAIService}
}

// UserConsent records that a user agreed to a version of an agreement.
type UserConsent struct {
	ID       uuid.UUID
	UserID   int
	Type     string
	Version  string
	AgreedAt time.Time
	IP       *string
}
