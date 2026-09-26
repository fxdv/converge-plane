package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSameOrigin(t *testing.T) {
	s := testService()
	s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.origins = trustedOrigins("http://localhost:3000", "https://api.example.com:443/")
	h := s.RequireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	session := &http.Cookie{Name: CookieAccessToken, Value: "x"}
	cases := []struct {
		name    string
		method  string
		cookie  *http.Cookie
		headers map[string]string
		want    int
	}{
		{"same-origin write", http.MethodPost, session, map[string]string{"Origin": "http://localhost:3000"}, http.StatusNoContent},
		{"api origin, default port normalized", http.MethodDelete, session, map[string]string{"Origin": "https://API.example.com"}, http.StatusNoContent},
		{"referer when origin is stripped", http.MethodPut, session, map[string]string{"Referer": "http://localhost:3000/ws/issues?x=1"}, http.StatusNoContent},
		{"foreign origin", http.MethodPost, session, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"sibling port is another origin", http.MethodPost, session, map[string]string{"Origin": "http://localhost:3001"}, http.StatusForbidden},
		{"opaque origin", http.MethodPost, session, map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"no origin and no referer", http.MethodPatch, session, nil, http.StatusForbidden},
		{"refresh cookie alone still counts", http.MethodPost, &http.Cookie{Name: CookieRefreshToken, Value: "x"}, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"safe method", http.MethodGet, session, map[string]string{"Origin": "https://evil.example"}, http.StatusNoContent},
		// Login CSRF: a cookie-less cross-site form POST redeeming the
		// attacker's sign-in code.
		{"foreign origin without cookies", http.MethodPost, nil, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"opaque origin without cookies", http.MethodPost, nil, map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"foreign referer without cookies", http.MethodPost, nil, map[string]string{"Referer": "https://evil.example/x"}, http.StatusForbidden},
		{"same-origin write without cookies", http.MethodPost, nil, map[string]string{"Origin": "http://localhost:3000"}, http.StatusNoContent},
		{"non-browser client: no cookies, no origin", http.MethodPost, nil, nil, http.StatusNoContent},
		{"authorization header", http.MethodPost, session, map[string]string{"Origin": "https://evil.example", "Authorization": "Bearer cvg_x"}, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "/api/v1/issues", nil)
			if c.cookie != nil {
				r.AddCookie(c.cookie)
			}
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}

func TestNormalizeOrigin(t *testing.T) {
	cases := map[string]string{
		"http://localhost:3000":      "http://localhost:3000",
		"http://localhost:3000/":     "http://localhost:3000",
		"HTTPS://Example.COM:443/a":  "https://example.com",
		"http://example.com:80":      "http://example.com",
		"http://[::1]:3000/x":        "http://[::1]:3000",
		"null":                       "",
		"":                           "",
		"/relative/path":             "",
		"https://example.com:8443/q": "https://example.com:8443",
	}
	for in, want := range cases {
		if got := normalizeOrigin(in); got != want {
			t.Errorf("normalizeOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}
