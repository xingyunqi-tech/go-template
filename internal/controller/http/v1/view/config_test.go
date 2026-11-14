package view_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appprofile"
	"github.com/xingyunqi-tech/go-template/internal/usecase/consent"
	"github.com/xingyunqi-tech/go-template/internal/usecase/region"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

func TestConfigView(t *testing.T) {
	t.Parallel()

	c := &appconfig.Config{Key: "k", Value: json.RawMessage(`{"b":1,"a":2}`), IsPublic: true, CreatedAt: nil, UpdatedAt: nil}
	assert.Equal(t, `{"key":"k","value":{"b":1,"a":2},"isPublic":true,"createdAt":null,"updatedAt":null}`, marshal(t, view.NewConfig(c)))
}

func TestRegionViews(t *testing.T) {
	t.Parallel()

	at := jsontime.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	parent := "110000000000"
	r := region.Region{ID: 2, Code: "110100000000", Name: "市辖区", Level: 2, ParentCode: &parent, Sort: 1, CreatedAt: at, UpdatedAt: at}
	fields := `"id":2,"code":"110100000000","name":"市辖区","level":2,"parentCode":"110000000000","sort":1,` +
		`"createdAt":"2026-01-02T03:04:05.000Z","updatedAt":"2026-01-02T03:04:05.000Z"`

	assert.Equal(t, `[{`+fields+`}]`, marshal(t, view.NewRegions([]region.Region{r})))
	assert.Equal(t, `[]`, marshal(t, view.NewRegions(nil)))

	tree := view.NewRegionTree([]*region.Node{{Region: r, Children: []*region.Node{}}})
	assert.Equal(t, `[{`+fields+`,"children":[]}]`, marshal(t, tree))
}

func TestAppProfileView(t *testing.T) {
	t.Parallel()

	name := "A"
	p := &appprofile.Profile{AppName: &name} //nolint:exhaustruct // unset fields are null
	assert.Equal(t, `{"appName":"A","slogan":null,"companyName":null,"copyright":null,"icpLicense":null,`+
		`"servicePhone":null,"serviceHours":null,"serviceEmail":null,"shareTitle":null}`, marshal(t, view.NewAppProfile(p)))
}

func TestConsentStatusView(t *testing.T) {
	t.Parallel()

	v, at := "v1", time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC)
	statuses := []consent.Status{
		{Type: "PRIVACY_POLICY", CurrentVersion: &v, AgreedVersion: &v, AgreedAt: &at, Agreed: true, NeedsConsent: false},
		{Type: "AI_SERVICE", CurrentVersion: nil, AgreedVersion: nil, AgreedAt: nil, Agreed: false, NeedsConsent: false},
	}

	assert.Equal(t, `[{"type":"PRIVACY_POLICY","currentVersion":"v1","agreedVersion":"v1","agreedAt":"2026-01-02T03:04:05.006Z","agreed":true,"needsConsent":false},`+
		`{"type":"AI_SERVICE","currentVersion":null,"agreedVersion":null,"agreedAt":null,"agreed":false,"needsConsent":false}]`,
		marshal(t, view.NewConsentStatuses(statuses)))
}
