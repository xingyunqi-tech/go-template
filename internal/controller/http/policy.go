package http

import (
	"slices"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// Auth is how an operation authenticates.
type Auth int

// Auth kinds.
const (
	// AuthPublic never reads Authorization.
	AuthPublic Auth = iota
	// AuthJWT requires a valid access token.
	AuthJWT
	// AuthOptional reads a valid access token and treats any other request as anonymous.
	AuthOptional
)

// Perms are the permission codes an operation requires.
type Perms struct {
	codes []string
	any   bool
}

// All requires every code.
func All(codes ...string) Perms {
	return Perms{codes: codes, any: false}
}

// Any requires one of the codes.
func Any(codes ...string) Perms {
	return Perms{codes: codes, any: true}
}

func (p Perms) empty() bool {
	return len(p.codes) == 0
}

func (p Perms) granted(a Authorizer, roles []string) bool {
	if a == nil {
		return false
	}

	has := func(code string) bool { return a.Granted(roles, code) }

	if p.any {
		return slices.ContainsFunc(p.codes, has)
	}

	for _, code := range p.codes {
		if !has(code) {
			return false
		}
	}

	return true
}

// Audit names the audit log entry written after a successful call: the resource id is the id
// path parameter, else the id of the response data.
type Audit struct {
	Action   string
	Resource string
}

// Policy is what the router does around one operation.
type Policy struct {
	Auth  Auth
	Perms Perms
	// Reauth requires auth_time within the last 10 minutes.
	Reauth bool
	// VerifiedEmail requires the email_verified claim.
	VerifiedEmail bool
	// RateLimits are shared rules counted per request before the handler.
	RateLimits []string
	// Tx runs the handler in a request transaction.
	Tx    bool
	Audit *Audit
	// BodyLimit overrides BODY_LIMIT; 0 keeps it.
	BodyLimit int64
}

const _userResource = "user"

// Policies returns the policy of every documented operation, keyed by operationId. The
// account, session and failures-only rules of platform conventions §11.6 are counted by the
// auth and user use cases. registerWithEmail and loginWithEmail run their own transactions.
func Policies() map[string]Policy {
	return map[string]Policy{
		"registerWithEmail":       {Auth: AuthPublic, RateLimits: []string{"auth-register"}},
		"loginWithEmail":          {Auth: AuthPublic, RateLimits: []string{"auth-login"}},
		"verifyEmail":             {Auth: AuthPublic, Tx: true},
		"resendVerificationEmail": {Auth: AuthJWT, RateLimits: []string{"auth-mail"}, Tx: true},
		"forgotPassword":          {Auth: AuthPublic, RateLimits: []string{"auth-mail"}, Tx: true},
		"resetPassword":           {Auth: AuthPublic, Tx: true, Audit: &Audit{Action: "user.password.reset", Resource: _userResource}},
		"refreshToken":            {Auth: AuthPublic, Tx: true},
		"logout":                  {Auth: AuthJWT, Tx: true},

		"getMe":               {Auth: AuthJWT},
		"updateMe":            {Auth: AuthJWT, Tx: true},
		"changePassword":      {Auth: AuthJWT, Tx: true, Audit: &Audit{Action: "user.password.change", Resource: _userResource}},
		"listMySessions":      {Auth: AuthJWT},
		"revokeMySession":     {Auth: AuthJWT, Tx: true},
		"revokeOtherSessions": {Auth: AuthJWT, Tx: true},

		"listUsers":       {Auth: AuthJWT, Perms: All(entity.PermUserRead)},
		"createUser":      {Auth: AuthJWT, Perms: All(entity.PermUserWrite), Tx: true, Audit: &Audit{Action: "user.create", Resource: _userResource}},
		"getUser":         {Auth: AuthJWT, Perms: All(entity.PermUserRead)},
		"updateUser":      {Auth: AuthJWT, Perms: All(entity.PermUserWrite), Tx: true, Audit: &Audit{Action: "user.update", Resource: _userResource}},
		"setUserRoles":    {Auth: AuthJWT, Perms: All(entity.PermRoleWrite), Tx: true, Audit: &Audit{Action: "user.roles.set", Resource: _userResource}},
		"listRoles":       {Auth: AuthJWT, Perms: All(entity.PermRoleRead)},
		"listPermissions": {Auth: AuthJWT, Perms: All(entity.PermRoleRead)},

		"listNotes":  {Auth: AuthJWT},
		"createNote": {Auth: AuthJWT, Tx: true, Audit: &Audit{Action: "note.create", Resource: "note"}},
		"getNote":    {Auth: AuthJWT},
		"updateNote": {Auth: AuthJWT, Tx: true, Audit: &Audit{Action: "note.update", Resource: "note"}},
		"deleteNote": {Auth: AuthJWT, Tx: true, Audit: &Audit{Action: "note.delete", Resource: "note"}},
	}
}
