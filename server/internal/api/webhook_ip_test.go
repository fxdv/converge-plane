package api

import (
	"net/netip"
	"testing"
)

// TestWebhookIPAllowed pins the SSRF filter on top of netx.PublicUnicast:
// dev mode admits loopback (mapped form included) and nothing else extra.
func TestWebhookIPAllowed(t *testing.T) {
	cases := []struct {
		ip   string
		dev  bool
		want bool
	}{
		{"203.0.113.9", false, true},
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, true},
		{"::ffff:127.0.0.1", true, true},
		{"::1", true, true},
		{"10.0.0.1", true, false},
		{"100.64.0.1", false, false},
		{"198.18.0.1", false, false},
		{"64:ff9b::a9fe:a9fe", false, false}, // NAT64 of 169.254.169.254
		{"::ffff:169.254.169.254", true, false},
	}
	for _, c := range cases {
		if got := webhookIPAllowed(netip.MustParseAddr(c.ip), c.dev); got != c.want {
			t.Errorf("webhookIPAllowed(%s, dev=%v) = %v, want %v", c.ip, c.dev, got, c.want)
		}
	}
}
