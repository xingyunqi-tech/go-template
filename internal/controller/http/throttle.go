package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_throttlerName      = "default"
	_ipv6SubnetPrefix   = 64
	_uploadOperation    = "uploadFile"
	_rateLimitLimit     = "X-RateLimit-Limit"
	_rateLimitRemaining = "X-RateLimit-Remaining"
	_rateLimitReset     = "X-RateLimit-Reset"
)

var _whitespace = regexp.MustCompile(`\s+`)

// Limiter consumes from the rate limit store.
type Limiter interface {
	Consume(ctx context.Context, key string, period time.Duration, limit int) ratelimit.Decision
}

// ThrottleOptions configures the global throttler guard (nestjs UserThrottlerGuard).
type ThrottleOptions struct {
	Enabled bool
	Limit   int
	TTL     time.Duration
	// AccessSecret verifies bearer tokens to count JWT routes per user.
	AccessSecret string
	FileDriver   string
	Limiter      Limiter
}

type throttler struct {
	opts       ThrottleOptions
	handlers   map[string]string
	trustProxy int
}

func newThrottler(opts ThrottleOptions, nestHandlers []byte, trustProxy int) (*throttler, error) {
	handlers := map[string]string{}
	if len(nestHandlers) == 0 {
		return &throttler{opts: opts, handlers: handlers, trustProxy: trustProxy}, nil
	}

	if err := json.Unmarshal(nestHandlers, &handlers); err != nil {
		return nil, fmt.Errorf("http: nest_handlers.json: %w", err)
	}

	return &throttler{opts: opts, handlers: handlers, trustProxy: trustProxy}, nil
}

func uploadHandler(driver string) string {
	switch driver {
	case "s3":
		return "FilesS3Controller-uploadFile"
	case "s3-presigned":
		return "FilesS3PresignedController-uploadFile"
	default:
		return "FilesLocalController-uploadFile"
	}
}

// handlerName is the nestjs Controller-handler of an operation; unknown operations use their id.
func (t *throttler) handlerName(operationID string) string {
	if operationID == _uploadOperation {
		return uploadHandler(t.opts.FileDriver)
	}

	if name, ok := t.handlers[operationID]; ok {
		return name
	}

	return operationID
}

// key is the nestjs throttler key with the GCRA suffix.
func (t *throttler) key(operationID, tracker string) string {
	sum := sha256.Sum256([]byte(t.handlerName(operationID) + "-" + _throttlerName + "-" + tracker))

	return "throttle:" + hex.EncodeToString(sum[:]) + ":" + _throttlerName + ":gcra"
}

// bearerUser is the id claim of a valid access token.
func (t *throttler) bearerUser(r *http.Request) (string, bool) {
	scheme, token := authorizationParts(r.Header.Get("Authorization"))
	if !strings.EqualFold(scheme, "bearer") || token == "" {
		return "", false
	}

	claims, err := jwtx.Verify(token, t.opts.AccessSecret)
	if err != nil || claims["id"] == nil {
		return "", false
	}

	return validatorx.ToString(claims["id"]), true
}

// tracker counts JWT routes with a valid bearer token per user, everything else per client IP.
func (t *throttler) tracker(r *http.Request, policy *Policy) string {
	if policy.Auth == AuthJWT {
		if id, ok := t.bearerUser(r); ok {
			return "user:" + id
		}
	}

	ip := ""
	if p := clientIP(r, t.trustProxy); p != nil {
		ip = *p
	}

	return "ip:" + NormalizeIP(ip)
}

// check consumes one request and writes the X-RateLimit-* headers, or fails with 429.
func (t *throttler) check(w http.ResponseWriter, r *http.Request, operationID string, policy *Policy) error {
	if t == nil || !t.opts.Enabled {
		return nil
	}

	limit, ttl := t.opts.Limit, t.opts.TTL
	if policy.Throttle != nil {
		limit, ttl = policy.Throttle.Limit, time.Duration(policy.Throttle.TTL)*time.Millisecond
	}

	d := t.opts.Limiter.Consume(r.Context(), t.key(operationID, t.tracker(r, policy)), ttl, limit)
	if !d.Allowed {
		return &httperr.TooManyRequests{Headers: http.Header{"Retry-After": []string{strconv.FormatInt(d.RetryAfterSeconds(), 10)}}}
	}

	h := w.Header()
	h.Set(_rateLimitLimit, strconv.Itoa(limit))
	h.Set(_rateLimitRemaining, strconv.FormatInt(d.Remaining, 10))
	h.Set(_rateLimitReset, strconv.FormatInt(d.ResetSeconds(), 10))

	return nil
}

// NormalizeIP is @nestjs/throttler normalizeIp: IPv6 addresses collapse to their /64 network,
// IPv4-mapped addresses become IPv4, and anything unparsable is returned unchanged.
func NormalizeIP(ip string) string {
	if ip == "" {
		return ip
	}

	bare, _, _ := strings.Cut(ip, "%")

	addr, err := netip.ParseAddr(bare)
	if err != nil {
		return ip
	}

	switch {
	case addr.Is4():
		return bare
	case addr == netip.IPv6Loopback():
		return "::1"
	case addr.Is4In6():
		return addr.Unmap().String()
	}

	prefix, err := addr.Prefix(_ipv6SubnetPrefix)
	if err != nil {
		return ip
	}

	return prefix.String()
}
