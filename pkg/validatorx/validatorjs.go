package validatorx

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Port of the validator.js 13.15.35 string validators; lengths and regular expressions work on UTF-16 code units.

var (
	errURIMalformed  = errors.New("URIError: URI malformed")
	errUUIDNotRegexp = errors.New("TypeError: uuid[version].test is not a function")
)

// options is a validator.js options object; missing, undefined and null keys take the default.
type options struct {
	obj *Object
}

func optionsOf(v Value) options {
	o, _ := v.(*Object)

	return options{obj: o}
}

func (o options) get(key string) Value {
	if o.obj == nil {
		return Undefined{}
	}

	v, ok := o.obj.Get(key)
	if !ok || v == nil {
		return Undefined{}
	}

	return v
}

func (o options) has(key string) bool {
	return !isUndefined(o.get(key))
}

func (o options) truthy(key string, def bool) bool {
	v := o.get(key)
	if isUndefined(v) {
		return def
	}

	return truthy(v)
}

func (o options) strings(key string) []string {
	arr, _ := o.get(key).(*Array)
	if arr == nil {
		return nil
	}

	out := make([]string, 0, len(arr.Items))

	for _, item := range arr.Items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func (o options) number(key string) (float64, bool) {
	f, ok := o.get(key).(float64)

	return f, ok
}

// isByteLength is validator.js isByteLength; hasOptions=false means no options argument.
func isByteLength(s string, opts options, hasOptions bool) (bool, error) {
	if !utf8.ValidString(s) {
		return false, errURIMalformed
	}

	if !hasOptions {
		return false, nil
	}

	minV := 0.0
	if v, ok := opts.number("min"); ok && truthy(v) {
		minV = v
	}

	length := float64(len(s))
	maxV, hasMax := opts.number("max")

	return length >= minV && (!hasMax || length <= maxV), nil
}

func byteLengthWithin(s string, minV, maxV float64) (bool, error) {
	if !utf8.ValidString(s) {
		return false, errURIMalformed
	}

	n := float64(len(s))

	return n >= minV && n <= maxV, nil
}

// jsLength is the isLength length: code units minus presentation sequences and surrogate pairs.
func jsLength(s string) int {
	units := toUnits(s)
	presentation := 0

	for i := 0; i < len(units); i++ {
		if units[i] != 0xFE0F && units[i] != 0xFE0E && i+1 < len(units) && (units[i+1] == 0xFE0F || units[i+1] == 0xFE0E) {
			presentation++
			i++
		}
	}

	pairs := 0

	for _, r := range s {
		if r > 0xFFFF {
			pairs++
		}
	}

	return len(units) - presentation - pairs
}

func isLength(s string, opts options) bool {
	minV := 0.0
	if v, ok := opts.number("min"); ok && truthy(v) {
		minV = v
	}

	length := float64(jsLength(s))
	maxV, hasMax := opts.number("max")
	inside := length >= minV && (!hasMax || length <= maxV)

	discrete, ok := opts.get("discreteLengths").(*Array)
	if inside && ok {
		return slices.ContainsFunc(discrete.Items, func(d Value) bool {
			f, isNum := d.(float64)

			return isNum && f == length
		})
	}

	return inside
}

type fqdnOptions struct {
	requireTLD       bool
	allowUnderscores bool
	allowTrailingDot bool
	allowNumericTLD  bool
	allowWildcard    bool
	ignoreMaxLength  bool
}

func fqdnOptionsOf(o options) fqdnOptions {
	return fqdnOptions{
		requireTLD:       o.truthy("require_tld", true),
		allowUnderscores: o.truthy("allow_underscores", false),
		allowTrailingDot: o.truthy("allow_trailing_dot", false),
		allowNumericTLD:  o.truthy("allow_numeric_tld", false),
		allowWildcard:    o.get("allow_wildcard") == true,
		ignoreMaxLength:  o.truthy("ignore_max_length", false),
	}
}

var (
	_fqdnTLD        = mustJSRegExp(`^([a-z\u00a1-\u00a8\u00aa-\ud7ff\uf900-\ufdcf\ufdf0-\uffef]{2,}|xn[a-z0-9-]{2,})$`, "i")
	_fqdnWhitespace = mustJSRegExp(`\s`, "")
	_fqdnNumeric    = mustJSRegExp(`^\d+$`, "")
	_fqdnPart       = mustJSRegExp(`^[a-z_\u00a1-\uffff0-9-]+$`, "i")
	_fqdnFullWidth  = mustJSRegExp(`[！-\uff5e]`, "")
	_fqdnHyphenEdge = mustJSRegExp(`^-|-$`, "")
)

const (
	_maxLabelLength = 63
	_wildcardPrefix = "*."
)

func isFQDN(s string, opts fqdnOptions) bool {
	if opts.allowTrailingDot && strings.HasSuffix(s, ".") {
		s = s[:len(s)-1]
	}

	if opts.allowWildcard && strings.HasPrefix(s, _wildcardPrefix) {
		s = s[len(_wildcardPrefix):]
	}

	parts := strings.Split(s, ".")
	tld := parts[len(parts)-1]

	if opts.requireTLD {
		if len(parts) < 2 {
			return false
		}

		if !opts.allowNumericTLD && !_fqdnTLD.test(tld) {
			return false
		}

		if _fqdnWhitespace.test(tld) {
			return false
		}
	}

	if !opts.allowNumericTLD && _fqdnNumeric.test(tld) {
		return false
	}

	for _, part := range parts {
		if utf16Len(part) > _maxLabelLength && !opts.ignoreMaxLength {
			return false
		}

		if !_fqdnPart.test(part) || _fqdnFullWidth.test(part) || _fqdnHyphenEdge.test(part) {
			return false
		}

		if !opts.allowUnderscores && strings.Contains(part, "_") {
			return false
		}
	}

	return true
}

const (
	_ipv4Segment = `(?:[0-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5])`
	_ipv4Address = `(` + _ipv4Segment + `[.]){3}` + _ipv4Segment
	_v6          = `(?:[0-9a-fA-F]{1,4})`
	_v4          = _ipv4Address
)

var (
	_ipv4Re = mustJSRegExp(`^`+_ipv4Address+`$`, "")
	_ipv6Re = mustJSRegExp(`^(`+
		`(?:`+_v6+`:){7}(?:`+_v6+`|:)|`+
		`(?:`+_v6+`:){6}(?:`+_v4+`|:`+_v6+`|:)|`+
		`(?:`+_v6+`:){5}(?::`+_v4+`|(:`+_v6+`){1,2}|:)|`+
		`(?:`+_v6+`:){4}(?:(:`+_v6+`){0,1}:`+_v4+`|(:`+_v6+`){1,3}|:)|`+
		`(?:`+_v6+`:){3}(?:(:`+_v6+`){0,2}:`+_v4+`|(:`+_v6+`){1,4}|:)|`+
		`(?:`+_v6+`:){2}(?:(:`+_v6+`){0,3}:`+_v4+`|(:`+_v6+`){1,5}|:)|`+
		`(?:`+_v6+`:){1}(?:(:`+_v6+`){0,4}:`+_v4+`|(:`+_v6+`){1,6}|:)|`+
		`(?::((?::`+_v6+`){0,5}:`+_v4+`|(?::`+_v6+`){1,7}|:))`+
		`)(%[0-9a-zA-Z.]{1,})?$`, "")
)

// isIP is validator.js isIP; a falsy version accepts both 4 and 6.
func isIP(s string, version Value) bool {
	if o, ok := version.(*Object); ok {
		version = options{obj: o}.get("version")
	}

	if !truthy(version) {
		return isIP(s, 4.0) || isIP(s, 6.0)
	}

	switch toString(version) {
	case "4":
		return _ipv4Re.test(s)
	case "6":
		return _ipv6Re.test(s)
	default:
		return false
	}
}

var (
	_splitNameAddress    = mustJSRegExp(`^([^\x00-\x1F\x7F-\x9F\cX]+)<`, "i")
	_emailUserPart       = mustJSRegExp(`^[a-z\d!#\$%&'\*\+\-\/=\?\^_`+"`"+`{\|}~]+$`, "i")
	_gmailUserPart       = mustJSRegExp(`^[a-z\d]+$`, "")
	_quotedEmailUser     = mustJSRegExp(`^([\s\x01-\x08\x0b\x0c\x0e-\x1f\x7f\x21\x23-\x5b\x5d-\x7e]|(\\[\x01-\x09\x0b\x0c\x0d-\x7f]))*$`, "i")
	_emailUserUTF8Part   = mustJSRegExp(`^[a-z\d!#\$%&'\*\+\-\/=\?\^_`+"`"+`{\|}~\u00a1-\ud7ff\uf900-\ufdcf\ufdf0-\uffef]+$`, "i")
	_quotedEmailUserUTF8 = mustJSRegExp(`^([\s\x01-\x08\x0b\x0c\x0e-\x1f\x7f\x21\x23-\x5b\x5d-\x7e\u00a0-\ud7ff\uf900-\ufdcf\ufdf0-\uffef]|(\\[\x01-\x09\x0b\x0c\x0d-\x7f\u00a0-\ud7ff\uf900-\ufdcf\ufdf0-\uffef]))*$`, "i")
	_displayNameQuotes   = mustJSRegExp(`^"(.+)"$`, "")
	_displayNameIllegal  = mustJSRegExp(`[\.";<>]`, "")
	_gmailDomains        = []string{"gmail.com", "googlemail.com"}
)

const (
	_maxEmailLength      = 254
	_maxEmailUserBytes   = 64
	_maxEmailDomainBytes = 254
	_gmailMinUserBytes   = 6
	_gmailMaxUserBytes   = 30
)

func validateDisplayName(displayName string) bool {
	withoutQuotes := displayName
	if groups, _ := _displayNameQuotes.exec(displayName); groups != nil {
		withoutQuotes = groups[1]
	}

	if jsTrim(withoutQuotes) == "" {
		return false
	}

	if _displayNameIllegal.test(withoutQuotes) {
		if withoutQuotes == displayName {
			return false
		}

		if strings.Count(withoutQuotes, `"`) != strings.Count(withoutQuotes, `\"`) {
			return false
		}
	}

	return true
}

func checkHost(host string, matches []string) bool {
	return slices.Contains(matches, host)
}

// isEmail is validator.js isEmail.
func isEmail(s string, opts options) (bool, error) {
	allowDisplay := opts.truthy("allow_display_name", false)
	requireDisplay := opts.truthy("require_display_name", false)

	if requireDisplay || allowDisplay {
		groups, _ := _splitNameAddress.exec(s)

		switch {
		case groups != nil:
			displayName := groups[1]
			s = strings.Replace(s, displayName, "", 1)
			s = strings.TrimPrefix(s, "<")
			s = strings.TrimSuffix(s, ">")
			displayName = strings.TrimSuffix(displayName, " ")

			if !validateDisplayName(displayName) {
				return false, nil
			}
		case requireDisplay:
			return false, nil
		}
	}

	ignoreMaxLength := opts.truthy("ignore_max_length", false)
	if !ignoreMaxLength && utf16Len(s) > _maxEmailLength {
		return false, nil
	}

	user, domain := "", s
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user, domain = s[:i], s[i+1:]
	}

	lowerDomain := jsLower(domain)

	if bl := opts.strings("host_blacklist"); len(bl) > 0 && checkHost(lowerDomain, bl) {
		return false, nil
	}

	if wl := opts.strings("host_whitelist"); len(wl) > 0 && !checkHost(lowerDomain, wl) {
		return false, nil
	}

	if ok, err := checkGmail(&user, lowerDomain, opts); !ok || err != nil {
		return false, err
	}

	if opts.get("ignore_max_length") != true {
		okUser, err := byteLengthWithin(user, 0, _maxEmailUserBytes)
		if err != nil {
			return false, err
		}

		okDomain, err := byteLengthWithin(domain, 0, _maxEmailDomainBytes)
		if err != nil {
			return false, err
		}

		if !okUser || !okDomain {
			return false, nil
		}
	}

	if !emailDomainValid(domain, opts) {
		return false, nil
	}

	if chars := opts.get("blacklisted_chars"); truthy(chars) {
		re, err := compileJSRegExp("["+toString(chars)+"]+", "")
		if err != nil {
			return false, err
		}

		if re.test(user) {
			return false, nil
		}
	}

	return emailUserValid(user, opts.truthy("allow_utf8_local_part", true)), nil
}

func checkGmail(user *string, lowerDomain string, opts options) (bool, error) {
	if !opts.truthy("domain_specific_validation", false) || !slices.Contains(_gmailDomains, lowerDomain) {
		return true, nil
	}

	*user = jsLower(*user)
	username, _, _ := strings.Cut(*user, "+")

	ok, err := byteLengthWithin(strings.ReplaceAll(username, ".", ""), _gmailMinUserBytes, _gmailMaxUserBytes)
	if !ok || err != nil {
		return false, err
	}

	for part := range strings.SplitSeq(username, ".") {
		if !_gmailUserPart.test(part) {
			return false, nil
		}
	}

	return true, nil
}

func emailDomainValid(domain string, opts options) bool {
	fqdn := fqdnOptions{
		requireTLD:       opts.truthy("require_tld", true),
		ignoreMaxLength:  opts.truthy("ignore_max_length", false),
		allowUnderscores: opts.truthy("allow_underscores", false),
		allowTrailingDot: false,
		allowNumericTLD:  false,
		allowWildcard:    false,
	}
	if isFQDN(domain, fqdn) {
		return true
	}

	if !opts.truthy("allow_ip_domain", false) {
		return false
	}

	if isIP(domain, Undefined{}) {
		return true
	}

	if !strings.HasPrefix(domain, "[") || !strings.HasSuffix(domain, "]") || len(domain) < 2 {
		return false
	}

	inner := domain[1 : len(domain)-1]

	return inner != "" && isIP(inner, Undefined{})
}

func emailUserValid(user string, allowUTF8 bool) bool {
	if strings.HasPrefix(user, `"`) && strings.HasSuffix(user, `"`) {
		inner := ""
		if len(user) >= 2 {
			inner = user[1 : len(user)-1]
		}

		if allowUTF8 {
			return _quotedEmailUserUTF8.test(inner)
		}

		return _quotedEmailUser.test(inner)
	}

	pattern := _emailUserPart
	if allowUTF8 {
		pattern = _emailUserUTF8Part
	}

	for part := range strings.SplitSeq(user, ".") {
		if !pattern.test(part) {
			return false
		}
	}

	return true
}

var (
	_wrappedIPv6       = mustJSRegExp(`^\[([^\]]+)\](?::([0-9]+))?$`, "")
	_urlForbidden      = mustJSRegExp(`[\s<>]`, "")
	_urlProtocol       = mustJSRegExp(`^([a-z][a-z0-9+\-.]*):`, "i")
	_urlValidAuth      = mustJSRegExp(`^[a-zA-Z0-9\-_.%:]*$`, "")
	_urlEncodedContent = mustJSRegExp(`%[0-9a-fA-F]{2}`, "")
	_urlLooksLikePort  = mustJSRegExp(`^[0-9]`, "")
	_urlPort           = mustJSRegExp(`^[0-9]+$`, "")
	_defaultProtocols  = []string{"http", "https", "ftp"}
)

const (
	_defaultMaxURLLength = 2084
	_maxPort             = 65535
	_maxPortDigits       = 6
)

type urlOptions struct {
	opts                      options
	protocols                 []string
	requireProtocol           bool
	requireHost               bool
	requirePort               bool
	requireValidProtocol      bool
	allowProtocolRelativeURLs bool
	allowFragments            bool
	allowQueryComponents      bool
	validateLength            bool
	maxAllowedLength          float64
	fqdn                      fqdnOptions
}

func urlOptionsOf(o options) urlOptions {
	protocols := _defaultProtocols
	if o.has("protocols") {
		protocols = o.strings("protocols")
	}

	maxLen, ok := o.number("max_allowed_length")
	if !ok {
		maxLen = _defaultMaxURLLength
	}

	return urlOptions{
		opts:                      o,
		protocols:                 protocols,
		requireProtocol:           o.truthy("require_protocol", false),
		requireHost:               o.truthy("require_host", true),
		requirePort:               o.truthy("require_port", false),
		requireValidProtocol:      o.truthy("require_valid_protocol", true),
		allowProtocolRelativeURLs: o.truthy("allow_protocol_relative_urls", false),
		allowFragments:            o.truthy("allow_fragments", true),
		allowQueryComponents:      o.truthy("allow_query_components", true),
		validateLength:            o.truthy("validate_length", true),
		maxAllowedLength:          maxLen,
		fqdn:                      fqdnOptionsOf(o),
	}
}

// isURL is validator.js isURL.
func isURL(s string, o options) bool {
	if s == "" || _urlForbidden.test(s) || strings.HasPrefix(s, "mailto:") {
		return false
	}

	opts := urlOptionsOf(o)

	if opts.validateLength && float64(utf16Len(s)) > opts.maxAllowedLength {
		return false
	}

	if !opts.allowFragments && strings.Contains(s, "#") {
		return false
	}

	if !opts.allowQueryComponents && strings.ContainsAny(s, "?&") {
		return false
	}

	s, _, _ = strings.Cut(s, "#")
	s, _, _ = strings.Cut(s, "?")

	s, explicit, ok := stripURLProtocol(s, opts)
	if !ok {
		return false
	}

	if strings.HasPrefix(s, "//") {
		if !explicit && !opts.allowProtocolRelativeURLs {
			return false
		}

		s = s[2:]
	}

	if s == "" {
		return false
	}

	s, _, _ = strings.Cut(s, "/")
	if s == "" && !opts.requireHost {
		return true
	}

	return checkURLAuthority(s, opts)
}

// stripURLProtocol removes the protocol prefix.
func stripURLProtocol(url string, opts urlOptions) (rest string, explicit, valid bool) {
	groups, _ := _urlProtocol.exec(url)
	if groups == nil {
		return url, false, !opts.requireProtocol
	}

	prefixLength := len(groups[0])
	afterColon := url[prefixLength:]

	cleanUp := func() (string, bool, bool) {
		protocol := jsLower(groups[1])
		if opts.requireValidProtocol && !slices.Contains(opts.protocols, protocol) {
			return "", true, false
		}

		return url[prefixLength:], true, true
	}

	if strings.HasPrefix(afterColon, "//") {
		return cleanUp()
	}

	beforeSlash, _, _ := strings.Cut(afterColon, "/")

	if at := strings.Index(beforeSlash, "@"); at != -1 {
		beforeAt := beforeSlash[:at]
		if _urlValidAuth.test(beforeAt) && !_urlEncodedContent.test(beforeAt) {
			return url, false, !opts.requireProtocol
		}

		return cleanUp()
	}

	if _urlLooksLikePort.test(afterColon) {
		return url, false, !opts.requireProtocol
	}

	return cleanUp()
}

func checkURLAuthority(authority string, opts urlOptions) bool {
	split := strings.Split(authority, "@")
	if len(split) > 1 {
		if opts.opts.truthy("disallow_auth", false) || split[0] == "" {
			return false
		}

		auth := split[0]
		split = split[1:]
		authParts := strings.Split(auth, ":")

		if strings.Contains(auth, ":") && len(authParts) > 2 {
			return false
		}

		if authParts[0] == "" && len(authParts) > 1 && authParts[1] == "" {
			return false
		}
	}

	hostname := strings.Join(split, "@")

	var host, portStr, ipv6 string

	hasPort := false

	if groups, matched := _wrappedIPv6.exec(hostname); groups != nil {
		ipv6 = groups[1]
		portStr, hasPort = groups[2], matched[2] && groups[2] != ""
	} else {
		var rest string

		host, rest, hasPort = strings.Cut(hostname, ":")
		portStr = rest
	}

	if hasPort && portStr != "" {
		if !_urlPort.test(portStr) {
			return false
		}

		if port := parsePort(portStr); port <= 0 || port > _maxPort {
			return false
		}
	} else if opts.requirePort {
		return false
	}

	if truthy(opts.opts.get("host_whitelist")) {
		return checkHost(host, opts.opts.strings("host_whitelist"))
	}

	if host == "" && !opts.requireHost {
		return true
	}

	if !isIP(host, Undefined{}) && !isFQDN(host, opts.fqdn) && (ipv6 == "" || !isIP(ipv6, 6.0)) {
		return false
	}

	if host == "" {
		host = ipv6
	}

	bl := opts.opts.strings("host_blacklist")

	return !truthy(opts.opts.get("host_blacklist")) || !checkHost(host, bl)
}

func parsePort(s string) float64 {
	digits := strings.TrimLeft(s, "0")
	if digits == "" {
		return 0
	}

	if len(digits) > _maxPortDigits {
		return math.Inf(1)
	}

	n, _ := strconv.Atoi(digits)

	return float64(n)
}

var (
	_uuidPatterns = map[string]*jsRegExp{
		"1":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-1[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"2":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-2[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"3":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-3[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"4":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"5":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-5[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"6":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-6[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"7":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-7[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"8":     mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-8[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`, "i"),
		"nil":   mustJSRegExp(`^00000000-0000-0000-0000-000000000000$`, "i"),
		"max":   mustJSRegExp(`^ffffffff-ffff-ffff-ffff-ffffffffffff$`, "i"),
		"loose": mustJSRegExp(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`, "i"),
		"all": mustJSRegExp(`^(?:[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`+
			`|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`, "i"),
	}
	_objectPrototypeKeys = []string{
		"__proto__", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__",
		"constructor", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString",
		"toString", "valueOf",
	}
)

// isUUID is validator.js isUUID; a version naming an Object.prototype property fails like in JS.
func isUUID(s string, version Value) (bool, error) {
	key := "all"
	if !isNullish(version) {
		key = toString(version)
	}

	if slices.Contains(_objectPrototypeKeys, key) {
		return false, errUUIDNotRegexp
	}

	re, ok := _uuidPatterns[key]

	return ok && re.test(s), nil
}

const _iso8601Body = `^([\+-]?\d{4}(?!\d{2}\b))((-?)((0[1-9]|1[0-2])(\3([12]\d|0[1-9]|3[01]))?|W([0-4]\d|5[0-3])(-?[1-7])?|(00[1-9]|0[1-9]\d|[12]\d{2}|3([0-5]\d|6[1-6])))(`

var (
	_iso8601 = mustJSRegExp(_iso8601Body+
		`[T\s]((([01]\d|2[0-3])((:?)[0-5]\d)?|24:?00)([\.,]\d+(?!:))?)?(\17[0-5]\d([\.,]\d+)?)?([zZ]|([\+-])([01]\d|2[0-3]):?([0-5]\d)?)?)?)?$`, "")
	_iso8601StrictSeparator = mustJSRegExp(_iso8601Body+
		`[T]((([01]\d|2[0-3])((:?)[0-5]\d)?|24:?00)([\.,]\d+(?!:))?)?(\17[0-5]\d([\.,]\d+)?)?([zZ]|([\+-])([01]\d|2[0-3]):?([0-5]\d)?)?)?)?$`, "")
	_ordinalDate = mustJSRegExp(`^(\d{4})-?(\d{3})([ T]{1}\.*|$)`, "")
	_dateParts   = mustJSRegExp(`(\d{4})-?(\d{0,2})-?(\d*)`, "")
	_daysInMonth = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
)

const (
	_leapYearDays    = 366
	_commonYearDays  = 365
	_minFullYear     = 1000
	_minTwoDigitYear = 100
	_maxDayDigits    = 2
	_february        = 2
)

func isLeapYear(year int) bool {
	return (year%4 == 0 && year%100 != 0) || year%400 == 0
}

// jsDateMatches reports whether new Date("Y-MM-DD") has the given UTC date under V8 parsing rules.
func jsDateMatches(year, month, day int) bool {
	if month < 1 || month > 12 {
		return false
	}

	days := _daysInMonth[month-1]
	if month == _february && isLeapYear(year) {
		days = 29
	}

	if day < 1 || day > days || year < _minTwoDigitYear {
		return false
	}

	if year >= _minFullYear {
		return true
	}

	_, offset := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local).Zone()

	return offset <= 0
}

func isValidISODate(s string) bool {
	if groups, _ := _ordinalDate.exec(s); groups != nil {
		year, _ := strconv.Atoi(groups[1])
		day, _ := strconv.Atoi(groups[2])

		if isLeapYear(year) {
			return day <= _leapYearDays
		}

		return day <= _commonYearDays
	}

	groups, _ := _dateParts.exec(s)
	if groups == nil {
		return true
	}

	year, _ := strconv.Atoi(groups[1])
	month, _ := strconv.Atoi(groups[2])
	dayDigits := strings.TrimLeft(groups[3], "0")

	if month != 0 && dayDigits != "" {
		day, _ := strconv.Atoi(dayDigits)

		return len(dayDigits) <= _maxDayDigits && jsDateMatches(year, month, day)
	}

	return true
}

// isISO8601 is validator.js isISO8601.
func isISO8601(s string, opts options) bool {
	pattern := _iso8601
	if opts.truthy("strictSeparator", false) {
		pattern = _iso8601StrictSeparator
	}

	if !pattern.test(s) {
		return false
	}

	if opts.truthy("strict", false) {
		return isValidISODate(s)
	}

	return true
}
