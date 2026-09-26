// Package netx resolves the client address of a request behind proxies.
//
// X-Forwarded-For is client-controlled on its left end: every proxy
// appends the address it received the connection from, so only entries
// added by proxies we trust are facts. The resolver walks the header
// from the right, skipping trusted proxies, and returns the first
// address it does not trust — the rightmost-untrusted rule. The leftmost
// entry is never trusted blindly.
package netx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies is loopback plus the private ranges (RFC 1918,
// RFC 4193): the reverse proxy and the web container of a compose or
// single-host deployment.
func DefaultTrustedProxies() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("fc00::/7"),
	}
}

// Resolver maps a request to its client IP.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver trusts the given proxy ranges; nil selects the defaults.
func NewResolver(trusted []netip.Prefix) *Resolver {
	if trusted == nil {
		trusted = DefaultTrustedProxies()
	}
	return &Resolver{trusted: trusted}
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIP returns the request's client address as a string.
func (r *Resolver) ClientIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	conn, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	conn = conn.Unmap()
	if !r.isTrusted(conn) {
		return conn.String()
	}
	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	leftmost := conn
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// A malformed hop ends the trustworthy chain.
			break
		}
		a = a.Unmap()
		if !r.isTrusted(a) {
			return a.String()
		}
		leftmost = a
	}
	return leftmost.String()
}

type ctxKey struct{}

// Middleware stores the resolved client IP on the request context.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, r.ClientIP(req))))
	})
}

// ClientIP returns the address stored by Middleware, falling back to the
// connection address when the request did not pass through it.
func ClientIP(req *http.Request) string {
	if v, ok := req.Context().Value(ctxKey{}).(string); ok && v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}
