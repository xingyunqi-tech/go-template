// Package appprofile is the mini program site profile (nestjs AppProfileService), stored as the
// config entry app.profile and replaced as a whole.
package appprofile

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// Profile holds the site profile; unset fields are nil.
type Profile struct {
	AppName      *string `json:"appName"`
	Slogan       *string `json:"slogan"`
	CompanyName  *string `json:"companyName"`
	Copyright    *string `json:"copyright"`
	IcpLicense   *string `json:"icpLicense"`
	ServicePhone *string `json:"servicePhone"`
	ServiceHours *string `json:"serviceHours"`
	ServiceEmail *string `json:"serviceEmail"`
	ShareTitle   *string `json:"shareTitle"`
}

func (p *Profile) fields() []**string {
	return []**string{
		&p.AppName, &p.Slogan, &p.CompanyName, &p.Copyright, &p.IcpLicense, &p.ServicePhone,
		&p.ServiceHours, &p.ServiceEmail, &p.ShareTitle,
	}
}

// normalize trims every field and unsets blank ones.
func (p *Profile) normalize() {
	for _, f := range p.fields() {
		if *f == nil {
			continue
		}

		if trimmed := validatorx.Trim(**f); trimmed != "" {
			*f = &trimmed
		} else {
			*f = nil
		}
	}
}

// fromJSON reads the string fields of a JSON object; anything else is unset.
func fromJSON(raw json.RawMessage) *Profile {
	p := &Profile{}

	var record map[string]json.RawMessage
	if json.Unmarshal(raw, &record) != nil {
		return p
	}

	names := []string{
		"appName", "slogan", "companyName", "copyright", "icpLicense", "servicePhone", "serviceHours",
		"serviceEmail", "shareTitle",
	}

	for i, f := range p.fields() {
		var s string
		if json.Unmarshal(record[names[i]], &s) == nil {
			*f = &s
		}
	}

	p.normalize()

	return p
}

// Service reads and replaces the profile.
type Service struct {
	config *appconfig.Service
}

// New returns a Service.
func New(config *appconfig.Service) *Service {
	return &Service{config: config}
}

// Get returns the stored profile.
func (s *Service) Get(ctx context.Context) (*Profile, error) {
	raw, _, err := s.config.Value(ctx, appconfig.KeyAppProfile)
	if err != nil {
		return nil, err //nolint:wrapcheck // config errors are wrapped
	}

	return fromJSON(raw), nil
}

// Update replaces the profile; nil fields are stored as null.
func (s *Service) Update(ctx context.Context, in Profile) (*Profile, error) {
	in.normalize()

	value, err := json.Marshal(map[string]*Profile{"value": &in})
	if err != nil {
		return nil, fmt.Errorf("appprofile: encode: %w", err)
	}

	if _, err := s.config.Upsert(ctx, appconfig.KeyAppProfile, value, nil); err != nil {
		return nil, err //nolint:wrapcheck // config errors are wrapped
	}

	return &in, nil
}
