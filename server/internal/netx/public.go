package netx

import "net/netip"

// nonPublic are special-purpose ranges that IsGlobalUnicast admits but
// that never name a host on the public internet: shared carrier space,
// benchmarking, IETF protocol assignments, the reserved class E block,
// and NAT64 prefixes, which translate to an embedded IPv4 address the
// v4 checks never see.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // RFC 6598 shared address space (CGNAT)
	netip.MustParsePrefix("192.0.0.0/24"),   // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // RFC 2544 benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // RFC 1112 reserved, incl. broadcast
	netip.MustParsePrefix("64:ff9b::/96"),   // RFC 6052 well-known NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"), // RFC 8215 local-use NAT64
}

// PublicUnicast reports whether ip is a unicast address on the public
// internet: the test for an outbound request to a user-supplied host
// (SSRF). IPv4-mapped IPv6 addresses are judged as the IPv4 address they
// carry.
func PublicUnicast(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
