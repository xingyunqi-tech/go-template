package view

import (
	"encoding/json"

	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appprofile"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

// Config is {key, value, isPublic, createdAt, updatedAt}.
type Config struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	IsPublic  bool            `json:"isPublic"`
	CreatedAt *jsontime.Time  `json:"createdAt"`
	UpdatedAt *jsontime.Time  `json:"updatedAt"`
}

// NewConfig returns the view of c.
func NewConfig(c *appconfig.Config) Config {
	return Config{Key: c.Key, Value: c.Value, IsPublic: c.IsPublic, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// AppProfile holds the nine site profile fields, null when unset.
type AppProfile struct {
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

// NewAppProfile returns the view of p.
func NewAppProfile(p *appprofile.Profile) AppProfile {
	return AppProfile(*p)
}
