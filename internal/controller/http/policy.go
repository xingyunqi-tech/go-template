package http

import (
	"maps"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// Auth is how an operation authenticates.
type Auth int

// Auth kinds.
const (
	AuthNone Auth = iota
	AuthJWT
	AuthOptionalJWT
	AuthRefresh
	AuthMonitor
)

func (a Auth) String() string {
	switch a {
	case AuthNone:
		return "none"
	case AuthJWT:
		return "jwt"
	case AuthOptionalJWT:
		return "optional-jwt"
	case AuthRefresh:
		return "refresh"
	case AuthMonitor:
		return "monitor-token"
	default:
		return "unknown"
	}
}

// Throttle overrides the default rate limit (nestjs @Throttle({default: {limit, ttl}}), ttl in ms).
type Throttle struct {
	Limit int
	TTL   int
}

// Audit is nestjs @Auditable: the entity id comes from the IDParams path parameters (joined with
// ":", default "id") or, with FromResponse first, from the response id; the recorded value is the
// validated body unless OmitBody, or the response with IncludeResponse.
type Audit struct {
	Action          string
	EntityType      string
	IDParams        []string
	FromResponse    bool
	OmitBody        bool
	IncludeResponse bool
}

// Policy is the per-operation behavior that nestjs declares with decorators.
type Policy struct {
	Auth Auth
	// RBAC routes go through the permission guard; Permissions are all required, or any one of
	// them when AnyPermission is set.
	RBAC          bool
	Permissions   []string
	AnyPermission bool
	// Query is the nestjs DTO validating the query string.
	Query string
	// Params is the nestjs DTO validating the path parameters (@Param() of a DTO class).
	Params   string
	Throttle *Throttle
	Audit    *Audit
	Tx       bool
	// Raw responses skip the envelope.
	Raw bool
	// Upload runs the upload purpose guard and receives the file before the query DTO.
	Upload bool
	// NoCache adds Cache-Control: no-cache, no-store, must-revalidate to 2xx responses.
	NoCache bool
}

// rbac is a JWT policy that requires permission.
func rbac(permission string, tx bool) Policy {
	return Policy{Auth: AuthJWT, RBAC: true, Permissions: []string{permission}, Tx: tx}
}

// Policies returns the policy of every implemented operation, keyed by operationId.
func Policies() map[string]Policy {
	policies := map[string]Policy{
		"getAppInfo":           {Auth: AuthNone},
		"simpleHealthCheck":    {Auth: AuthNone},
		"healthCheck":          {Auth: AuthMonitor, NoCache: true},
		"livenessProbe":        {Auth: AuthNone, NoCache: true},
		"readinessProbe":       {Auth: AuthNone, NoCache: true},
		"getMetrics":           {Auth: AuthMonitor, Raw: true},
		"uploadFile":           {Auth: AuthJWT, Query: "UploadFileQueryDto", Upload: true},
		"readPrivateFile":      {Auth: AuthJWT, Raw: true},
		"downloadFile":         {Auth: AuthNone, Raw: true},
		"loginWithEmail":       {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 60_000}, Tx: true},
		"registerWithEmail":    {Auth: AuthNone, Tx: true},
		"confirmEmail":         {Auth: AuthNone, Tx: true},
		"confirmNewEmail":      {Auth: AuthNone, Tx: true},
		"forgotPassword":       {Auth: AuthNone, Throttle: &Throttle{Limit: 3, TTL: 300_000}, Tx: true},
		"resetPassword":        {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 300_000}, Tx: true},
		"getCurrentUser":       {Auth: AuthJWT},
		"updateCurrentUser":    {Auth: AuthJWT, Throttle: &Throttle{Limit: 10, TTL: 60_000}, Tx: true},
		"deleteCurrentUser":    {Auth: AuthJWT, Tx: true},
		"refreshToken":         {Auth: AuthRefresh, Tx: true},
		"logout":               {Auth: AuthJWT, Tx: true},
		"changePassword":       {Auth: AuthJWT, Throttle: &Throttle{Limit: 3, TTL: 60_000}, Tx: true},
		"getMySessions":        {Auth: AuthJWT},
		"revokeMySession":      {Auth: AuthJWT, Tx: true},
		"revokeOtherSessions":  {Auth: AuthJWT, Tx: true},
		"sendSmsCode":          {Auth: AuthNone, Throttle: &Throttle{Limit: 3, TTL: 60_000}},
		"loginWithPhone":       {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 60_000}, Tx: true},
		"loginWithSms":         {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 60_000}, Tx: true},
		"registerWithPhone":    {Auth: AuthNone, Throttle: &Throttle{Limit: 3, TTL: 60_000}, Tx: true},
		"resetPasswordByPhone": {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 300_000}, Tx: true},
		"loginWithWechat":      {Auth: AuthNone, Throttle: &Throttle{Limit: 10, TTL: 60_000}},
		"loginWithWechatPhone": {Auth: AuthNone, Throttle: &Throttle{Limit: 10, TTL: 60_000}},
		"loginWithAlipay":      {Auth: AuthNone, Throttle: &Throttle{Limit: 10, TTL: 60_000}},
		"createUser":           rbac(entity.PermUserCreate, true),
		"getUsers":             {Auth: AuthJWT, RBAC: true, Permissions: []string{entity.PermUserList}, Query: "QueryUserDto"},
		"getUser":              rbac(entity.PermUserRead, false),
		"updateUser":           rbac(entity.PermUserUpdate, true),
		"deleteUser":           rbac(entity.PermUserDelete, true),
	}

	maps.Copy(policies, modulePolicies())

	return policies
}

// modulePolicies returns the policies of the general-purpose modules.
func modulePolicies() map[string]Policy {
	return map[string]Policy{
		"getNotifications":             {Auth: AuthJWT, Query: "QueryNotificationDto"},
		"getUnreadCount":               {Auth: AuthJWT},
		"getNotification":              {Auth: AuthJWT},
		"markNotificationAsRead":       {Auth: AuthJWT, Tx: true},
		"markAllNotificationsAsRead":   {Auth: AuthJWT, Tx: true},
		"deleteNotification":           {Auth: AuthJWT, Tx: true},
		"getNotificationSettings":      {Auth: AuthJWT},
		"updateNotificationSettings":   {Auth: AuthJWT, Tx: true},
		"adminRetriggerAccountCleanup": rbac(entity.PermUserDelete, true),
		"getAddresses":                 {Auth: AuthJWT, Query: "QueryAddressDto"},
		"getDefaultAddress":            {Auth: AuthJWT},
		"getAddress":                   {Auth: AuthJWT},
		"createAddress":                {Auth: AuthJWT, Tx: true},
		"updateAddress":                {Auth: AuthJWT, Tx: true},
		"deleteAddress":                {Auth: AuthJWT, Tx: true},
		"setDefaultAddress":            {Auth: AuthJWT, Tx: true},
		"getFavorites":                 {Auth: AuthJWT, Query: "QueryFavoriteDto"},
		"createFavorite":               {Auth: AuthJWT, Tx: true},
		"deleteFavorite":               {Auth: AuthJWT, Tx: true},
		"toggleFavorite":               {Auth: AuthJWT, Tx: true},
		"checkFavorite":                {Auth: AuthJWT},
		"createFeedback":               {Auth: AuthJWT, Tx: true},
		"getFeedbacks":                 {Auth: AuthJWT, Query: "QueryFeedbackDto"},
		"getFeedback":                  {Auth: AuthJWT},
		"getConfig":                    {Auth: AuthNone},
		"getConfigAsAdmin":             {Auth: AuthJWT, RBAC: true, Permissions: []string{entity.PermSystemConfig}},
		"upsertConfig":                 {Auth: AuthJWT, RBAC: true, Permissions: []string{entity.PermSystemConfig}, Tx: true},
		"getProvinces":                 {Auth: AuthNone},
		"getCities":                    {Auth: AuthNone, Params: "ProvinceCodeParam"},
		"getDistricts":                 {Auth: AuthNone, Params: "CityCodeParam"},
		"getRegionByCode":              {Auth: AuthNone, Params: "RegionCodeParam"},
		"getRegionTree":                {Auth: AuthNone},
		"queryRegions":                 {Auth: AuthNone, Query: "QueryRegionDto"},
		"getAppProfile":                {Auth: AuthNone},
		"adminUpdateAppProfile":        {Auth: AuthJWT, RBAC: true, Permissions: []string{entity.PermSystemConfig}, Tx: true},
		"getMyConsents":                {Auth: AuthJWT},
		"agreeConsent":                 {Auth: AuthJWT, Tx: true},
	}
}

// Undocumented lists the operations nestjs excludes from the OpenAPI document.
func Undocumented() []string {
	return []string{"getMetrics", "readPrivateFile", "downloadFile"}
}
