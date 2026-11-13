package http

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_maxID          = 2_147_483_647
	_maxQueryKeys   = 1000
	_hexDigitsAfter = 2
)

var (
	_idPattern = regexp.MustCompile(`^\d+$`)

	errMalformedURI = errors.New("URIError: URI malformed")
	errPolicy       = errors.New("operation policy")
)

// Guard authenticates a request for one Auth kind and returns the request carrying the identity.
type Guard func(r *http.Request) (*http.Request, error)

// PermissionGuard checks the RBAC permissions of a policy.
type PermissionGuard func(r *http.Request, policy Policy) error

// pipeline runs, in front of the generated handlers, what nestjs does between routing and the
// handler: path parameter decoding, guards, ParseIdPipe and the global ValidationPipe.
type pipeline struct {
	ops        map[string]*operation
	policies   map[string]Policy
	guards     map[Auth]Guard
	permission PermissionGuard
	validator  *validatorx.Validator
	errs       *errorWriter
}

// check reports operations that are registered but cannot run: missing policy, missing guard,
// or a request DTO absent from validation.json.
func (p *pipeline) check(registered []*operation) error {
	var errs []error

	for _, op := range registered {
		errs = append(errs, p.checkOperation(op)...)
	}

	return errors.Join(errs...)
}

func (p *pipeline) checkOperation(op *operation) []error {
	policy, ok := p.policies[op.id]
	if !ok {
		return []error{fmt.Errorf("%w: %s has no policy", errPolicy, op.id)}
	}

	var errs []error

	if policy.Auth != AuthNone && p.guards[policy.Auth] == nil {
		errs = append(errs, fmt.Errorf("%w: %s needs a %s guard", errPolicy, op.id, policy.Auth))
	}

	if needsPermission(&policy) && p.permission == nil {
		errs = append(errs, fmt.Errorf("%w: %s needs the permission guard", errPolicy, op.id))
	}

	for _, dto := range []string{op.bodyDTO, policy.Query} {
		if dto != "" && !p.validator.Has(dto) {
			errs = append(errs, fmt.Errorf("%w: %s uses %s, which is not in validation.json", errPolicy, op.id, dto))
		}
	}

	return errs
}

func needsPermission(policy *Policy) bool {
	return policy.RBAC || len(policy.Permissions) > 0
}

func (p *pipeline) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		match := matchedRoute(r)
		if match == nil {
			if raw, failed := undecodable(r); failed {
				p.errs.write(w, r, paramDecodeError(raw))

				return
			}

			next.ServeHTTP(w, r)

			return
		}

		op := p.ops[operationKey(match.method, match.pattern)]
		if op == nil {
			next.ServeHTTP(w, r)

			return
		}

		prepared, err := p.prepare(r, op, match)
		if err != nil {
			p.errs.write(w, r, err)

			return
		}

		next.ServeHTTP(w, prepared)
	})
}

func (p *pipeline) prepare(r *http.Request, op *operation, match *routeMatch) (*http.Request, error) {
	params, err := decodeParams(op, match)
	if err != nil {
		return r, err
	}

	policy := p.policies[op.id]

	if r, err = p.guard(r, &policy); err != nil {
		return r, err
	}

	for _, param := range op.pathParams {
		if param.numeric && !validID(params[param.name]) {
			return r, httperr.InvalidID()
		}
	}

	if op.bodyDTO != "" {
		if err := p.validateBody(r, op.bodyDTO); err != nil {
			return r, err
		}
	}

	if policy.Query != "" {
		if err := p.validateQuery(r, policy.Query); err != nil {
			return r, err
		}
	}

	return r, nil
}

// decodeParams decodes path parameters like Express does while routing.
func decodeParams(op *operation, match *routeMatch) (map[string]string, error) {
	params := make(map[string]string, len(op.pathParams))

	for _, param := range op.pathParams {
		raw := match.param(param.name)

		decoded, err := decodeURIComponent(raw)
		if err != nil {
			return nil, paramDecodeError(raw)
		}

		params[param.name] = decoded
	}

	return params, nil
}

func paramDecodeError(raw string) error {
	return httperr.New(http.StatusBadRequest, "Failed to decode param '"+raw+"'")
}

// guard runs the authentication guard and then the permission guard of the policy.
func (p *pipeline) guard(r *http.Request, policy *Policy) (*http.Request, error) {
	if policy.Auth != AuthNone {
		authed, err := p.guards[policy.Auth](r)
		if err != nil {
			return r, err
		}

		r = authed
	}

	if needsPermission(policy) {
		if err := p.permission(r, *policy); err != nil {
			return r, err
		}
	}

	return r, nil
}

func (p *pipeline) validateBody(r *http.Request, dto string) error {
	value, err := p.validator.Run(dto, "body", reqctx.Body(r.Context()))
	if err != nil {
		return err
	}

	body, _ := validatorx.Stringify(value)
	r.Body = io.NopCloser(bytes.NewReader([]byte(body)))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", _jsonMediaType)

	return nil
}

func (p *pipeline) validateQuery(r *http.Request, dto string) error {
	value, err := p.validator.Run(dto, "query", parseQueryString(r.URL.RawQuery))
	if err != nil {
		return err
	}

	r.URL.RawQuery = encodeQuery(value)

	return nil
}

// validID is nestjs ParseIdPipe: digits only, 1 <= id <= 2147483647.
func validID(s string) bool {
	if !_idPattern.MatchString(s) {
		return false
	}

	n, err := strconv.ParseUint(s, 10, 64)

	return err == nil && n >= 1 && n <= _maxID
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// decodeURIComponent follows the JavaScript function: malformed escapes and invalid UTF-8 fail.
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}

	buf := make([]byte, 0, len(s))

	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			buf = append(buf, s[i])

			continue
		}

		if i+_hexDigitsAfter >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", errMalformedURI
		}

		b, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
		buf = append(buf, byte(b))
		i += _hexDigitsAfter
	}

	if !utf8.Valid(buf) {
		return "", errMalformedURI
	}

	return string(buf), nil
}

// queryUnescape is Node querystring.unescape: decodeURIComponent, else byte-wise decoding.
func queryUnescape(s string) string {
	if decoded, err := decodeURIComponent(s); err == nil {
		return decoded
	}

	buf := make([]byte, 0, len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+_hexDigitsAfter < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			buf = append(buf, byte(b))
			i += _hexDigitsAfter

			continue
		}

		buf = append(buf, s[i])
	}

	return validatorx.DecodeUTF8Lossy(buf)
}

// parseQueryString is Node querystring.parse: "+" is a space, repeated keys become arrays, and
// at most 1000 pairs are read.
func parseQueryString(query string) *validatorx.Object {
	out := validatorx.NewObject()
	if query == "" {
		return out
	}

	pairs := strings.Split(query, "&")
	if len(pairs) > _maxQueryKeys {
		pairs = pairs[:_maxQueryKeys]
	}

	for _, pair := range pairs {
		if pair == "" {
			continue
		}

		key, value, _ := strings.Cut(pair, "=")
		name := queryUnescape(strings.ReplaceAll(key, "+", " "))
		decoded := queryUnescape(strings.ReplaceAll(value, "+", " "))

		existing, ok := out.Get(name)
		if !ok {
			out.Set(name, decoded)

			continue
		}

		if arr, isArray := existing.(*validatorx.Array); isArray {
			arr.Items = append(arr.Items, decoded)
		} else {
			out.Set(name, &validatorx.Array{Items: []validatorx.Value{existing, decoded}})
		}
	}

	return out
}

// encodeQuery writes the validated query back for the generated parameter binding; objects are
// JSON, arrays repeat the key, null and undefined are omitted.
func encodeQuery(value validatorx.Value) string {
	o, ok := value.(*validatorx.Object)
	if !ok {
		return ""
	}

	var parts []string

	add := func(key string, v validatorx.Value) {
		s, ok := queryText(v)
		if ok {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(s))
		}
	}

	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if arr, isArray := v.(*validatorx.Array); isArray {
			for _, item := range arr.Items {
				add(k, item)
			}

			continue
		}

		add(k, v)
	}

	return strings.Join(parts, "&")
}

func queryText(v validatorx.Value) (string, bool) {
	switch x := v.(type) {
	case nil, validatorx.Undefined:
		return "", false
	case string:
		return x, true
	case *validatorx.Object, *validatorx.Array:
		return validatorx.Stringify(x)
	default:
		return validatorx.ToString(x), true
	}
}
