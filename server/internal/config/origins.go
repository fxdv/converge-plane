package config

import (
	"net/url"
	"strings"
)

// TrustedRequestOrigins lists Origin header values accepted for
// cookie-authenticated writes and CORS. Production uses the configured
// web and API URLs; dev mode also trusts any loopback host on the web
// port (tunnel via IP, localhost vs 127.0.0.1).
func TrustedRequestOrigins(c Config) []string {
	web := strings.TrimRight(strings.TrimSpace(c.WebOrigin), "/")
	pub := strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	out := []string{web, pub}
	out = append(out, loopbackNameAliases(web)...)
	out = append(out, loopbackNameAliases(pub)...)
	if c.DevMode {
		out = append(out, devLoopbackPortOrigins(web)...)
		// Compose publishes web on :3000 and API on :3001 even when
		// CONVERGE_WEB_ORIGIN names another loopback port (Bravo tunnel).
		for _, port := range []string{"3000", "3001"} {
			out = append(out, loopbackHostVariants("http", port)...)
		}
	}
	return out
}

// loopbackNameAliases adds localhost, 127.0.0.1, and [::1] variants for
// the same scheme and port when the URL is already loopback.
func loopbackNameAliases(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || !isLoopbackURL(raw) {
		return nil
	}
	return loopbackHostVariants(u.Scheme, u.Port())
}

// devLoopbackPortOrigins trusts every loopback hostname on the web app's
// port so CONVERGE_WEB_ORIGIN can stay a canonical URL while developers
// open the app via 127.0.0.1 or an SSH tunnel to the server IP.
func devLoopbackPortOrigins(webOrigin string) []string {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(webOrigin), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return loopbackHostVariants(u.Scheme, u.Port())
}

func loopbackHostVariants(scheme, port string) []string {
	scheme = strings.ToLower(scheme)
	suffix := hostPortSuffix(scheme, port)
	hosts := []string{"localhost", "127.0.0.1", "[::1]"}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, scheme+"://"+h+suffix)
	}
	return out
}

func hostPortSuffix(scheme, port string) string {
	if port == "" {
		return ""
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return ""
	}
	return ":" + port
}
