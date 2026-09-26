package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// RequireSameOrigin refuses state-changing requests that ride on session
// cookies unless the browser reports they come from the web app or the
// API's own origin (Origin, or Referer when a privacy setting strips
// Origin). SameSite=Lax keeps the cookies off cross-site POSTs, but not
// off requests from a sibling subdomain, which is same-site.
//
// Requests without session cookies carry no ambient authority and pass,
// as do requests with an Authorization header: a cross-origin page cannot
// set one without a CORS preflight, which only the web origin passes.
func (s *Service) RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) || r.Header.Get("Authorization") != "" || !hasSessionCookie(r) {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if !s.origins[normalizeOrigin(origin)] {
			authEvents.With("cross_origin_refused").Inc()
			s.log.Warn("cross-origin request refused",
				"method", r.Method, "path", r.URL.Path, "origin", origin)
			writeError(w, http.StatusForbidden, "CROSS_ORIGIN", "request origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func hasSessionCookie(r *http.Request) bool {
	for _, name := range []string{CookieAccessToken, CookieRefreshToken} {
		if _, err := r.Cookie(name); err == nil {
			return true
		}
	}
	return false
}

// trustedOrigins is the set of origins allowed to send cookie-authenticated
// writes, normalized like the header values they are compared with.
func trustedOrigins(raw ...string) map[string]bool {
	out := map[string]bool{}
	for _, o := range raw {
		if n := normalizeOrigin(o); n != "" {
			out[n] = true
		}
	}
	return out
}

// normalizeOrigin reduces an origin or URL to "scheme://host[:port]",
// lowercased and without a default port; "" when it has neither.
func normalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}
