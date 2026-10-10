package netx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	res := NewResolver(nil)
	cases := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"direct public client, header ignored", req("203.0.113.9:5000", "1.2.3.4"), "203.0.113.9"},
		{"behind loopback proxy", req("127.0.0.1:40000", "198.51.100.7"), "198.51.100.7"},
		{"behind ipv6 loopback proxy", req("[::1]:40000", "198.51.100.7"), "198.51.100.7"},
		{"spoofed leftmost entry is skipped", req("127.0.0.1:40000", "1.2.3.4, 198.51.100.7, 127.0.0.2"), "198.51.100.7"},
		{"lan client cannot spoof by default", req("192.168.1.20:1", "198.51.100.7"), "192.168.1.20"},
		{"private hop is the client by default", req("127.0.0.1:1", "198.51.100.7, 10.0.0.5"), "10.0.0.5"},
		{"docker bridge is not trusted by default", req("172.21.0.2:1", "198.51.100.7"), "172.21.0.2"},
		{"multiple header lines", req("127.0.0.1:1", "1.2.3.4", "198.51.100.7"), "198.51.100.7"},
		{"all hops trusted: leftmost", req("127.0.0.1:1", "127.0.0.5, ::1"), "127.0.0.5"},
		{"no header behind proxy", req("127.0.0.1:1"), "127.0.0.1"},
		{"malformed hop stops the walk", req("127.0.0.1:1", "198.51.100.7, garbage"), "127.0.0.1"},
		{"ipv4-mapped ipv6", req("[::ffff:203.0.113.9]:1"), "203.0.113.9"},
		{"ipv4-mapped loopback proxy", req("[::ffff:127.0.0.1]:1", "198.51.100.7"), "198.51.100.7"},
	}
	for _, c := range cases {
		if got := res.ClientIP(c.r); got != c.want {
			t.Errorf("%s: ClientIP = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCustomTrustedProxies(t *testing.T) {
	res := NewResolver([]netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")})
	if got := res.ClientIP(req("203.0.113.9:1", "198.51.100.7")); got != "198.51.100.7" {
		t.Fatalf("custom trusted proxy: %q", got)
	}
	if got := res.ClientIP(req("10.0.0.1:1", "198.51.100.7")); got != "10.0.0.1" {
		t.Fatalf("private range must not be trusted when not configured: %q", got)
	}
}

func TestMiddlewareStoresClientIP(t *testing.T) {
	res := NewResolver(nil)
	var got string
	h := res.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIP(r) }))
	h.ServeHTTP(httptest.NewRecorder(), req("127.0.0.1:1", "198.51.100.7"))
	if got != "198.51.100.7" {
		t.Fatalf("ClientIP from context = %q", got)
	}
	if ip := ClientIP(req("203.0.113.9:1")); ip != "203.0.113.9" {
		t.Fatalf("fallback without middleware = %q", ip)
	}
}

func TestComposeSubnetTrusted(t *testing.T) {
	// docker-compose.yaml pins its network and trusts it explicitly: the
	// web container's hop is believed, a LAN address is not.
	res := NewResolver([]netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("172.31.250.0/24"),
	})
	if got := res.ClientIP(req("172.31.250.3:1", "198.51.100.7")); got != "198.51.100.7" {
		t.Fatalf("compose web hop: %q", got)
	}
	if got := res.ClientIP(req("192.168.1.20:1", "198.51.100.7")); got != "192.168.1.20" {
		t.Fatalf("LAN client outside the compose subnet: %q", got)
	}
}
