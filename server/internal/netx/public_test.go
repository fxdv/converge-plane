package netx

import (
	"net/netip"
	"testing"
)

func TestPublicUnicast(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"203.0.113.9", true},
		{"8.8.8.8", true},
		{"2606:4700::1111", true},
		{"::ffff:8.8.8.8", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"100.128.0.1", true},
		{"198.18.0.1", false},
		{"198.19.255.254", false},
		{"198.20.0.1", true},
		{"192.0.0.8", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},
		{"64:ff9b::a00:1", false},   // NAT64 of 10.0.0.1
		{"64:ff9b::808:808", false}, // NAT64 of 8.8.8.8: still refused
		{"64:ff9b:1::1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"::ffff:100.64.0.1", false},
		{"::ffff:198.18.0.1", false},
	}
	for _, c := range cases {
		if got := PublicUnicast(netip.MustParseAddr(c.ip)); got != c.want {
			t.Errorf("PublicUnicast(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}
