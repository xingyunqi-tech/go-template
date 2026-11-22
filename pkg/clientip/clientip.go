// Package clientip resolves the client IP behind trusted proxies.
package clientip

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

const _ipv6SubjectBits = 64

var (
	errEmptyCIDR    = errors.New("clientip: empty CIDR")
	errNegativeHops = errors.New("clientip: negative hop count")
)

// Proxies is TRUSTED_PROXIES: a hop count, or the CIDRs of the trusted proxies.
type Proxies struct {
	hops     int
	prefixes []netip.Prefix
}

// Parse reads a non-negative hop count or a comma-separated CIDR list.
func Parse(s string) (Proxies, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Proxies{}, nil
	}

	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return Proxies{}, fmt.Errorf("%w: %d", errNegativeHops, n)
		}

		return Proxies{hops: n}, nil
	}

	var prefixes []netip.Prefix

	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return Proxies{}, errEmptyCIDR
		}

		p, err := netip.ParsePrefix(part)
		if err != nil {
			addr, addrErr := netip.ParseAddr(part)
			if addrErr != nil {
				return Proxies{}, fmt.Errorf("clientip: %w", err)
			}

			p = netip.PrefixFrom(addr, addr.BitLen())
		}

		prefixes = append(prefixes, p.Masked())
	}

	return Proxies{prefixes: prefixes}, nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *Proxies) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}

	*p = parsed

	return nil
}

func (p Proxies) trusted(addr netip.Addr) bool {
	for _, prefix := range p.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

// ClientIP is the peer address, or with trusted proxies the X-Forwarded-For entry taken from
// the right: hops entries for a count, the first untrusted one for CIDRs.
func (p Proxies) ClientIP(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}

	var forwarded []string

	for _, header := range r.Header.Values("X-Forwarded-For") {
		for part := range strings.SplitSeq(header, ",") {
			if part = strings.TrimSpace(part); part != "" {
				forwarded = append(forwarded, part)
			}
		}
	}

	slices.Reverse(forwarded)
	chain := append([]string{peer}, forwarded...)

	if len(p.prefixes) == 0 {
		return chain[min(p.hops, len(chain)-1)]
	}

	for _, ip := range chain[:len(chain)-1] {
		addr, err := netip.ParseAddr(ip)
		if err != nil || !p.trusted(addr.Unmap()) {
			return ip
		}
	}

	return chain[len(chain)-1]
}

// Subject is the rate limit subject of ip: IPv4 as is, IPv6 truncated to its /64.
func Subject(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}

	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}

	return netip.PrefixFrom(addr, _ipv6SubjectBits).Masked().String()
}
