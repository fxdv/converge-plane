package auth

import "converge/internal/config"

// TrustedOrigins is the normalized set of browser origins allowed to
// send cookie-authenticated writes and pass MCP origin checks.
func TrustedOrigins(cfg config.Config) map[string]bool {
	return trustedOrigins(config.TrustedRequestOrigins(cfg)...)
}
