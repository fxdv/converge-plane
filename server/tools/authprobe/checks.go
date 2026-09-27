package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const evilOrigin = "https://evil.example"

func (p *probe) runChecks() {
	p.check("HDR-1", "responses are nosniff, API responses are not cacheable, no server banner", p.checkHeaders)
	p.check("HDR-2", "CORS admits only the web origin", p.checkCORS)
	p.check("HDR-3", "metrics are not served on the public listener", p.checkMetricsHidden)
	p.check("HDR-4", "error bodies carry no internals", p.checkErrorHygiene)
	p.check("HDR-5", "a client-supplied request id is bounded before it is echoed and logged", p.checkRequestID)

	p.check("SIGNIN-1", "create-code answers alike for new and known addresses and returns no link", p.checkEnumerationParity)
	p.check("SIGNIN-2", "malformed and header-injecting addresses are refused", p.checkEmailValidation)
	p.check("SIGNIN-3", "the link reaches the inbox only, points at the web origin, code in the fragment", p.checkLinkShape)
	p.check("SIGNIN-4", "a code works only with its own pre-auth session", p.checkCodeBinding)
	p.check("SIGNIN-5", "session cookies are HttpOnly, SameSite=Lax, host-only, Secure as configured", p.checkCookieFlags)
	p.check("SIGNIN-6", "a code works once", p.checkSingleUse)
	p.check("SIGNIN-7", "signing in burns the address's other outstanding codes", p.checkBurnsOthers)
	p.check("SIGNIN-8", "resend rotates the code; a used pre-auth session cannot be re-armed", p.checkResend)
	p.check("SIGNIN-9", "a foreign page cannot sign the browser in (login CSRF)", p.checkLoginCSRF)
	p.check("SIGNIN-10", "a foreign page cannot make the API send sign-in mail", p.checkForeignCreateCode)

	p.check("RL-1", "code guessing is throttled per client", p.checkGuessThrottle)
	p.check("RL-2", "code issuance is throttled per address across clients", p.checkEmailThrottle)
	p.check("RL-3", "spoofed X-Forwarded-For hops do not reset the budget", p.checkXFFSpoof)

	p.check("SESS-1", "tampered access tokens are refused", p.checkTamper)
	p.check("SESS-2", "access and refresh tokens are not interchangeable", p.checkKindConfusion)
	p.check("SESS-3", "cookie refresh needs the anti-csrf token, and rotates", p.checkRefreshCSRF)
	p.check("SESS-4", "a replayed refresh token revokes the session", p.checkRefreshReuse)
	p.check("SESS-5", "sign-out kills the access and refresh tokens", p.checkSignout)
	p.check("SESS-6", "cookie writes from a foreign or unknown origin are refused", p.checkCookieWriteOrigin)
	p.check("SESS-7", "Bearer writes need no origin (not ambient authority)", p.checkBearerWrite)

	p.check("AUTHZ-1", "unauthenticated reads are refused", p.checkUnauthenticated)
	p.check("AUTHZ-2", "another workspace's data and issues are unreachable", p.checkCrossWorkspace)
	p.check("AUTHZ-3", "a scoped agent token reaches only its scopes and teams, and dies on revocation", p.checkScopedToken)
	p.check("AUTHZ-4", "MCP tool calls are held to the token's scopes and teams; sessions and foreign origins are refused", p.checkMCPScopes)

	if p.webhookSecret != "" {
		p.check("HOOK-1", "GitHub webhook deliveries count only when signed with the shared secret", p.checkGitHubWebhook)
	}
}

// ---- hygiene ---------------------------------------------------------------

func (p *probe) checkHeaders() error {
	for _, path := range []string{"/healthz", "/api/auth/session", "/api/v1/users"} {
		res, err := p.do(request{method: "GET", path: path})
		if err != nil {
			return err
		}
		if got := res.header.Get("X-Content-Type-Options"); got != "nosniff" {
			return fmt.Errorf("%s: X-Content-Type-Options %q, want nosniff", path, got)
		}
		if cc := res.header.Get("Cache-Control"); strings.HasPrefix(path, "/api/") && !strings.Contains(cc, "no-store") {
			return fmt.Errorf("%s: Cache-Control %q, want no-store (responses carry tokens and tenant data)", path, cc)
		}
		for _, h := range []string{"Server", "X-Powered-By"} {
			if v := res.header.Get(h); v != "" {
				return fmt.Errorf("%s: %s header %q", path, h, v)
			}
		}
	}
	return nil
}

func (p *probe) checkCORS() error {
	preflight := func(origin string) (response, error) {
		return p.do(request{method: "OPTIONS", path: "/api/v1/users", header: map[string]string{
			"Origin":                         origin,
			"Access-Control-Request-Method":  "PUT",
			"Access-Control-Request-Headers": "content-type",
		}})
	}
	res, err := preflight(evilOrigin)
	if err != nil {
		return err
	}
	if v := res.header.Get("Access-Control-Allow-Origin"); v != "" {
		return fmt.Errorf("preflight from %s allowed: Access-Control-Allow-Origin %q", evilOrigin, v)
	}
	res, err = p.do(request{method: "GET", path: "/api/auth/session", header: map[string]string{"Origin": evilOrigin}})
	if err != nil {
		return err
	}
	if v := res.header.Get("Access-Control-Allow-Origin"); v != "" {
		return fmt.Errorf("GET from %s readable: Access-Control-Allow-Origin %q", evilOrigin, v)
	}
	res, err = preflight(p.webOrigin)
	if err != nil {
		return err
	}
	if res.header.Get("Access-Control-Allow-Origin") != p.webOrigin || res.header.Get("Access-Control-Allow-Credentials") != "true" {
		return fmt.Errorf("preflight from the web origin not admitted: %v", res.header)
	}
	return nil
}

func (p *probe) checkMetricsHidden() error {
	res, err := p.do(request{method: "GET", path: "/metrics"})
	if err != nil {
		return err
	}
	if res.status == http.StatusOK || strings.Contains(string(res.body), "converge_") {
		return fmt.Errorf("/metrics answered on the API listener: %s", res)
	}
	return nil
}

var internals = regexp.MustCompile(`(?i)pgx|sqlstate|postgres|goroutine|panic|runtime error|\.go:\d+|/(usr|home|root|app)/|syntax error at`)

func (p *probe) checkErrorHygiene() error {
	probes := []request{
		{method: "POST", path: "/api/auth/signinup/code", body: "{not json", ctype: "application/json"},
		{method: "POST", path: "/api/auth/signinup/code/consume", body: `{"linkCode": 12}`, ctype: "application/json"},
		{method: "POST", path: "/api/auth/signinup/code/resend", json: map[string]string{"preAuthSessionId": "' or 1=1 --"}},
		{method: "GET", path: "/api/v1/sync_actions/delta?workspaceId=not-a-uuid&lastSequenceId=x"},
		{method: "GET", path: "/api/v1/../../etc/passwd"},
		{method: "GET", path: "/nope"},
	}
	for _, rq := range probes {
		res, err := p.do(rq)
		if err != nil {
			return err
		}
		if res.status >= 500 {
			return fmt.Errorf("%s %s: server error %s", rq.method, rq.path, res)
		}
		if internals.Match(res.body) {
			return fmt.Errorf("%s %s leaks internals: %s", rq.method, rq.path, res)
		}
	}
	return nil
}

func (p *probe) checkRequestID() error {
	for _, id := range []string{strings.Repeat("A", 4096), "abc<script>alert(1)</script>", "x\u202eevil"} {
		res, err := p.do(request{method: "GET", path: "/healthz", header: map[string]string{"X-Request-Id": id}})
		if err != nil {
			return err
		}
		echo := res.header.Get("X-Request-Id")
		if len(echo) > 128 || strings.ContainsAny(echo, "<>\"'") || !isPrintableASCII(echo) {
			return fmt.Errorf("request id %q echoed as %q (unbounded ids reach every log line)", clip(id), clip(echo))
		}
	}
	return nil
}

// ---- sign-in ---------------------------------------------------------------

func (p *probe) checkEnumerationParity() error {
	known, err := p.signIn()
	if err != nil {
		return fmt.Errorf("sign in the known address: %w", err)
	}
	shape := func(email string) (string, response, error) {
		res, err := p.do(request{method: "POST", path: "/api/auth/signinup/code", json: map[string]string{"email": email}})
		if err != nil {
			return "", res, err
		}
		var m map[string]any
		_ = json.Unmarshal(res.body, &m)
		var keys []string
		for k, v := range m {
			if s, ok := v.(string); ok && (strings.Contains(s, "://") || strings.Contains(s, "#")) {
				return "", res, fmt.Errorf("create-code response leaks a link in %q: %s", k, res)
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Sprintf("%d %v %v", res.status, keys, m["status"]), res, nil
	}
	a, resA, err := shape(known.email)
	if err != nil {
		return err
	}
	b, _, err := shape(newEmail())
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("known address answers %s, new address %s", a, b)
	}
	if resA.field("devMagicLink") != "" {
		return fmt.Errorf("devMagicLink returned: the server runs in dev mode")
	}
	return nil
}

func (p *probe) checkEmailValidation() error {
	for _, email := range []string{
		"x\r\nSubject: pwned\r\n@probe.test",
		"x\nBcc: victim@probe.test",
		"a b@probe.test",
		"a\x00b@probe.test",
		"Name <a@probe.test>",
		"a@b@probe.test",
		"@probe.test",
		"a@probe",
	} {
		res, err := p.do(request{method: "POST", path: "/api/auth/signinup/code", json: map[string]string{"email": email}})
		if err != nil {
			return err
		}
		if res.status != http.StatusBadRequest || res.field("error") != "INVALID_EMAIL" {
			return fmt.Errorf("address %q accepted: %s", email, res)
		}
	}
	return nil
}

func (p *probe) checkLinkShape() error {
	email := newEmail()
	link, err := p.requestCode(email)
	if err != nil {
		return err
	}
	if link.origin != p.webOrigin {
		return fmt.Errorf("link points at %s, want the web origin %s", link.origin, p.webOrigin)
	}
	if len(link.code) < 20 {
		return fmt.Errorf("code %q is short: guessable", link.code)
	}
	if len(link.preAuthSessionID) < 32 {
		return fmt.Errorf("preAuthSessionId %q is short", link.preAuthSessionID)
	}
	return nil
}

func (p *probe) checkCodeBinding() error {
	link, err := p.requestCode(newEmail())
	if err != nil {
		return err
	}
	res, err := p.consume(signInLink{code: link.code, preAuthSessionID: randHex(20)})
	if err != nil {
		return err
	}
	if res.field("status") != "INVALID_LINK_CODE" || res.cookie(cookieAccess) != nil {
		return fmt.Errorf("code accepted under a foreign pre-auth session: %s", res)
	}
	res, err = p.consume(link)
	if err != nil {
		return err
	}
	if res.field("status") != "OK" {
		return fmt.Errorf("code unusable with its own session after a mismatched try: %s", res)
	}
	return nil
}

func (p *probe) checkCookieFlags() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	for _, name := range []string{cookieAccess, cookieRefresh, cookieAntiCsrf, cookieFront} {
		c, ok := s.jar[name]
		if !ok {
			return fmt.Errorf("cookie %s not set", name)
		}
		wantHTTPOnly := name == cookieAccess || name == cookieRefresh
		switch {
		case c.HttpOnly != wantHTTPOnly:
			return fmt.Errorf("%s: HttpOnly=%v, want %v", name, c.HttpOnly, wantHTTPOnly)
		case c.SameSite != http.SameSiteLaxMode && c.SameSite != http.SameSiteStrictMode:
			return fmt.Errorf("%s: SameSite=%v, want Lax or Strict", name, c.SameSite)
		case c.Secure != p.secureCookies:
			return fmt.Errorf("%s: Secure=%v, want %v", name, c.Secure, p.secureCookies)
		case c.Domain != "":
			return fmt.Errorf("%s: Domain=%q widens the cookie to sibling hosts", name, c.Domain)
		}
	}
	return nil
}

func (p *probe) checkSingleUse() error {
	link, err := p.requestCode(newEmail())
	if err != nil {
		return err
	}
	if res, err := p.consume(link); err != nil || res.field("status") != "OK" {
		return fmt.Errorf("first use: %v %s", err, res)
	}
	res, err := p.consume(link)
	if err != nil {
		return err
	}
	if res.field("status") != "INVALID_LINK_CODE" || res.cookie(cookieAccess) != nil {
		return fmt.Errorf("second use of the same link: %s", res)
	}
	return nil
}

func (p *probe) checkBurnsOthers() error {
	email := newEmail()
	older, err := p.requestCode(email)
	if err != nil {
		return err
	}
	newer, err := p.requestCode(email)
	if err != nil {
		return err
	}
	if res, err := p.consume(newer); err != nil || res.field("status") != "OK" {
		return fmt.Errorf("newer link: %v %s", err, res)
	}
	res, err := p.consume(older)
	if err != nil {
		return err
	}
	if res.field("status") != "INVALID_LINK_CODE" {
		return fmt.Errorf("older link still works after signing in: %s", res)
	}
	return nil
}

func (p *probe) checkResend() error {
	email := newEmail()
	first, err := p.requestCode(email)
	if err != nil {
		return err
	}
	resend := func() (response, error) {
		return p.do(request{method: "POST", path: "/api/auth/signinup/code/resend",
			json: map[string]string{"preAuthSessionId": first.preAuthSessionID}})
	}
	res, err := resend()
	if err != nil {
		return err
	}
	if res.status != http.StatusOK || res.field("devMagicLink") != "" {
		return fmt.Errorf("resend: %s", res)
	}
	msg, err := p.mail.wait(email, 2, 10*time.Second)
	if err != nil {
		return err
	}
	second, err := parseLink(msg)
	if err != nil {
		return err
	}
	if second.code == first.code || second.preAuthSessionID != first.preAuthSessionID {
		return fmt.Errorf("resend did not rotate the code within the session: %+v then %+v", first, second)
	}
	if res, _ := p.consume(first); res.field("status") != "INVALID_LINK_CODE" {
		return fmt.Errorf("the replaced code still works: %s", res)
	}
	if res, _ := p.consume(second); res.field("status") != "OK" {
		return fmt.Errorf("the resent code does not work: %s", res)
	}
	res, err = resend()
	if err != nil {
		return err
	}
	if res.status != http.StatusBadRequest || res.field("error") != "EXPIRED_PRE_AUTH_SESSION" {
		return fmt.Errorf("resend after sign-in re-armed the session: %s", res)
	}
	return nil
}

// checkLoginCSRF: the attacker requests a link for their own address and
// has a victim's browser redeem it from a hostile page, which can send a
// cross-site form POST (urlencoded or text/plain) with no preflight. If
// that works, the victim works inside the attacker's account.
func (p *probe) checkLoginCSRF() error {
	variants := []struct {
		name   string
		origin string
		build  func(l signInLink) request
	}{
		{"urlencoded form, code in the query", evilOrigin, func(l signInLink) request {
			return request{method: "POST", path: "/api/auth/signinup/code/consume?code=" + url.QueryEscape(l.code),
				body: "x=1", ctype: "application/x-www-form-urlencoded"}
		}},
		{"text/plain form carrying JSON", evilOrigin, func(l signInLink) request {
			return request{method: "POST", path: "/api/auth/signinup/code/consume",
				body:  fmt.Sprintf(`{"linkCode":%q,"preAuthSessionId":%q,"pad":"="}`, l.code, l.preAuthSessionID),
				ctype: "text/plain"}
		}},
		{"sandboxed frame (Origin: null)", "null", func(l signInLink) request {
			return request{method: "POST", path: "/api/auth/signinup/code/consume?code=" + url.QueryEscape(l.code),
				body: "x=1", ctype: "application/x-www-form-urlencoded"}
		}},
	}
	for _, v := range variants {
		link, err := p.requestCode(newEmail()) // the attacker's own inbox
		if err != nil {
			return err
		}
		rq := v.build(link)
		rq.header = map[string]string{"Origin": v.origin}
		res, err := p.do(rq)
		if err != nil {
			return err
		}
		if res.status != http.StatusForbidden || res.cookie(cookieAccess) != nil {
			return fmt.Errorf("%s from %s: %s (session cookies set: %v)", v.name, v.origin, res, res.cookie(cookieAccess) != nil)
		}
	}
	return nil
}

func (p *probe) checkForeignCreateCode() error {
	victim := newEmail()
	res, err := p.do(request{method: "POST", path: "/api/auth/signinup/code",
		body: fmt.Sprintf(`{"email":%q}`, victim), ctype: "text/plain",
		header: map[string]string{"Origin": evilOrigin}})
	if err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	if res.status != http.StatusForbidden || p.mail.count(victim) > 0 {
		return fmt.Errorf("cross-site create-code: %s, mail sent: %v", res, p.mail.count(victim) > 0)
	}
	return nil
}

// ---- throttling ------------------------------------------------------------

func (p *probe) guess(xff string) (response, error) {
	return p.do(request{method: "POST", path: "/api/auth/signinup/code/consume", xff: xff, allow429: true,
		json: map[string]string{"linkCode": strings.ToUpper(randHex(10)), "preAuthSessionId": randHex(20)}})
}

func expectThrottled(res response) error {
	if res.header.Get("Retry-After") == "" || res.field("error") != "RATE_LIMITED" {
		return fmt.Errorf("429 without Retry-After or RATE_LIMITED: %s %v", res, res.header)
	}
	return nil
}

func (p *probe) checkGuessThrottle() error {
	for i := 1; i <= 45; i++ {
		res, err := p.guess("")
		if err != nil {
			return err
		}
		if res.status == http.StatusTooManyRequests {
			return expectThrottled(res)
		}
		if res.field("status") != "INVALID_LINK_CODE" {
			return fmt.Errorf("guess %d: %s", i, res)
		}
	}
	return fmt.Errorf("45 wrong codes from one client, none throttled")
}

func (p *probe) checkEmailThrottle() error {
	email := newEmail()
	for i := 1; i <= 8; i++ {
		res, err := p.do(request{method: "POST", path: "/api/auth/signinup/code", xff: randomIP(), allow429: true,
			json: map[string]string{"email": email}})
		if err != nil {
			return err
		}
		if res.status == http.StatusTooManyRequests {
			if i <= 5 {
				return fmt.Errorf("throttled after %d codes: the per-address budget is 5", i-1)
			}
			return expectThrottled(res)
		}
		if res.status != http.StatusOK {
			return fmt.Errorf("code %d: %s", i, res)
		}
	}
	return fmt.Errorf("8 codes for one address from 8 clients, none throttled")
}

// checkXFFSpoof: the client prepends a fresh fake hop to every request;
// the trusted proxy (the probe) appends the real address. The API must
// key on the hop its proxy vouches for.
func (p *probe) checkXFFSpoof() error {
	real := randomIP()
	for i := 1; i <= 45; i++ {
		res, err := p.guess(randomIP() + ", " + real)
		if err != nil {
			return err
		}
		if res.status == http.StatusTooManyRequests {
			return expectThrottled(res)
		}
	}
	return fmt.Errorf("45 guesses behind rotating spoofed hops, none throttled")
}

// ---- sessions --------------------------------------------------------------

// retoken re-encodes a token payload with one field changed, keeping the
// signature.
func retoken(tok string, edit func(map[string]any)) string {
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return tok
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return tok
	}
	edit(m)
	out, _ := json.Marshal(m)
	return base64.StdEncoding.EncodeToString(out)
}

func (p *probe) checkTamper() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	tok := s.inner(cookieAccess)
	if st, err := p.me(nil, tok); err != nil || st != http.StatusOK {
		return fmt.Errorf("untouched access token refused: %d %v", st, err)
	}
	other, err := p.signIn()
	if err != nil {
		return err
	}
	for name, forged := range map[string]string{
		"account swapped":   retoken(tok, func(m map[string]any) { m["up"] = other.accountID }),
		"expiry extended":   retoken(tok, func(m map[string]any) { m["ate"] = m["ate"].(float64) + 86400000 }),
		"signature dropped": retoken(tok, func(m map[string]any) { delete(m, "hs") }),
		"signature blanked": retoken(tok, func(m map[string]any) { m["hs"] = "" }),
		"session swapped":   retoken(tok, func(m map[string]any) { m["sid"] = "00000000-0000-4000-8000-000000000000" }),
	} {
		if st, err := p.me(nil, forged); err != nil || st != http.StatusUnauthorized {
			return fmt.Errorf("%s: HTTP %d %v, want 401", name, st, err)
		}
	}
	return nil
}

func (p *probe) checkKindConfusion() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	if st, err := p.me(nil, s.inner(cookieRefresh)); err != nil || st != http.StatusUnauthorized {
		return fmt.Errorf("refresh token accepted as an access token: HTTP %d %v", st, err)
	}
	res, err := p.do(request{method: "POST", path: "/api/auth/session/refresh", bearer: s.inner(cookieAccess)})
	if err != nil {
		return err
	}
	if res.status != http.StatusUnauthorized {
		return fmt.Errorf("access token accepted by refresh: %s", res)
	}
	return nil
}

func (p *probe) checkRefreshCSRF() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	for name, token := range map[string]string{"no anti-csrf": "", "wrong anti-csrf": randHex(16)} {
		res, err := p.do(request{method: "POST", path: "/api/auth/session/refresh",
			cookies: s.cookies(cookieRefresh), header: map[string]string{"anti-csrf": token}})
		if err != nil {
			return err
		}
		if res.status != http.StatusUnauthorized {
			return fmt.Errorf("cookie refresh with %s: %s", name, res)
		}
	}
	old := s.inner(cookieRefresh)
	res, err := p.refresh(s)
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return err
	}
	s.absorb(res.cookies)
	if s.inner(cookieRefresh) == old {
		return fmt.Errorf("refresh did not rotate the refresh token")
	}
	if st, _ := p.me(s.cookies(cookieAccess), ""); st != http.StatusOK {
		return fmt.Errorf("refreshed access token refused: HTTP %d", st)
	}
	return nil
}

func (p *probe) checkRefreshReuse() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	stolen := s.cookies(cookieRefresh)
	for range 2 {
		res, err := p.refresh(s)
		if err != nil {
			return err
		}
		if err := expectStatus(res, http.StatusOK); err != nil {
			return err
		}
		s.absorb(res.cookies)
	}
	res, err := p.do(request{method: "POST", path: "/api/auth/session/refresh",
		cookies: stolen, header: map[string]string{"anti-csrf": s.antiCsrf()}})
	if err != nil {
		return err
	}
	if res.status != http.StatusUnauthorized {
		return fmt.Errorf("replayed refresh token accepted: %s", res)
	}
	if res, _ := p.refresh(s); res.status != http.StatusUnauthorized {
		return fmt.Errorf("session survived the replay: current refresh token still works: %s", res)
	}
	if st, _ := p.me(s.cookies(cookieAccess), ""); st != http.StatusUnauthorized {
		return fmt.Errorf("session survived the replay: access token still works: HTTP %d", st)
	}
	return nil
}

func (p *probe) checkSignout() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	kept := s.all()
	res, err := p.do(request{method: "POST", path: "/api/auth/signout", cookies: kept})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return err
	}
	if st, _ := p.me(s.cookies(cookieAccess), ""); st != http.StatusUnauthorized {
		return fmt.Errorf("access token works after sign-out: HTTP %d", st)
	}
	res, err = p.do(request{method: "POST", path: "/api/auth/session/refresh",
		cookies: s.cookies(cookieRefresh), header: map[string]string{"anti-csrf": s.antiCsrf()}})
	if err != nil {
		return err
	}
	if res.status != http.StatusUnauthorized {
		return fmt.Errorf("refresh token works after sign-out: %s", res)
	}
	return nil
}

// onboardingProbe is a cookie-authenticated write with no side effect:
// the empty body fails validation (422) once the request is let through.
func (p *probe) onboardingProbe(s *session, header map[string]string, bearer string) (response, error) {
	rq := request{method: "POST", path: "/api/v1/workspaces/onboarding", json: map[string]string{}, header: header, bearer: bearer}
	if s != nil {
		rq.cookies = s.all()
	}
	return p.do(rq)
}

func (p *probe) checkCookieWriteOrigin() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	for name, h := range map[string]map[string]string{
		"foreign Origin":                 {"Origin": evilOrigin},
		"Origin: null":                   {"Origin": "null"},
		"sibling port":                   {"Origin": siblingPort(p.webOrigin)},
		"foreign Referer, no Origin":     {"Origin": "", "Referer": evilOrigin + "/page"},
		"neither Origin nor Referer":     {"Origin": ""},
		"web origin as a Referer prefix": {"Origin": "", "Referer": p.webOrigin + ".evil.example/"},
	} {
		res, err := p.onboardingProbe(s, h, "")
		if err != nil {
			return err
		}
		if res.status != http.StatusForbidden || res.field("error") != "CROSS_ORIGIN" {
			return fmt.Errorf("%s: %s", name, res)
		}
	}
	res, err := p.onboardingProbe(s, nil, "")
	if err != nil {
		return err
	}
	if res.status == http.StatusForbidden {
		return fmt.Errorf("the web origin itself is refused: %s", res)
	}
	return nil
}

func (p *probe) checkBearerWrite() error {
	s, err := p.signIn()
	if err != nil {
		return err
	}
	res, err := p.onboardingProbe(nil, map[string]string{"Origin": ""}, s.inner(cookieAccess))
	if err != nil {
		return err
	}
	return expectStatus(res, http.StatusUnprocessableEntity)
}

// ---- authorization ---------------------------------------------------------

func (p *probe) checkUnauthenticated() error {
	ws := "00000000-0000-4000-8000-000000000000"
	for _, path := range []string{
		"/api/v1/users",
		"/api/v1/sync_actions/bootstrap?workspaceId=" + ws + "&modelNames=Issue",
		"/api/v1/sync_actions/delta?workspaceId=" + ws + "&lastSequenceId=0",
		"/api/v1/sync_actions/stream?workspaceId=" + ws,
	} {
		res, err := p.do(request{method: "GET", path: path})
		if err != nil {
			return err
		}
		if res.status != http.StatusUnauthorized {
			return fmt.Errorf("GET %s without credentials: %s", path, res)
		}
	}
	return nil
}

type syncRecord struct {
	ModelName string         `json:"modelName"`
	Data      map[string]any `json:"data"`
}

func (p *probe) checkCrossWorkspace() error {
	owner, err := p.signIn()
	if err != nil {
		return err
	}
	intruder, err := p.signIn()
	if err != nil {
		return err
	}
	ws, teamID, stateID, err := p.newWorkspace(owner)
	if err != nil {
		return err
	}
	issue := map[string]string{"teamId": teamID, "stateId": stateID, "title": "probe"}
	issueID, _, err := p.createIssue(owner, issue)
	if err != nil {
		return err
	}

	for _, rq := range []request{
		{method: "GET", path: "/api/v1/sync_actions/bootstrap?workspaceId=" + ws + "&modelNames=Issue,Team"},
		{method: "GET", path: "/api/v1/sync_actions/delta?workspaceId=" + ws + "&lastSequenceId=0"},
		{method: "GET", path: "/api/v1/sync_actions/stream?workspaceId=" + ws},
		{method: "GET", path: "/api/v1/issues/" + issueID + "/relations"},
		{method: "POST", path: "/api/v1/issues/" + issueID + "/pull_requests",
			json: map[string]string{"url": "https://github.com/acme/app/pull/1"}},
		{method: "DELETE", path: "/api/v1/issues/" + issueID + "/pull_requests/00000000-0000-4000-8000-000000000001"},
		{method: "POST", path: "/api/v1/issues/" + issueID, json: map[string]string{"title": "pwned"}},
		{method: "POST", path: "/api/v1/issues/" + issueID + "/move", json: map[string]string{"teamId": teamID}},
		{method: "DELETE", path: "/api/v1/issues/" + issueID},
		{method: "POST", path: "/api/v1/issues", json: issue},
	} {
		rq.cookies = intruder.all()
		res, err := p.do(rq)
		if err != nil {
			return fmt.Errorf("%s %s: %w", rq.method, rq.path, err)
		}
		if res.status != http.StatusNotFound {
			return fmt.Errorf("intruder %s %s: %s, want 404", rq.method, rq.path, res)
		}
	}
	return nil
}

// checkScopedToken: an agent token issued with scopes and a team grant
// reaches its own lane and nothing else, and dies when revoked.
func (p *probe) checkScopedToken() error {
	owner, err := p.signIn()
	if err != nil {
		return err
	}
	ws, team1, state1, err := p.newWorkspace(owner)
	if err != nil {
		return err
	}
	res, err := p.do(request{method: "POST", path: "/api/v1/teams", cookies: owner.all(),
		json: map[string]string{"name": "Other", "identifier": "OTH", "workspaceId": ws}})
	if err != nil {
		return err
	}
	if res.status != http.StatusOK && res.status != http.StatusCreated {
		return fmt.Errorf("create team: %s", res)
	}
	team2 := res.field("id")
	res, err = p.do(request{method: "POST", path: "/api/v1/" + team2 + "/workflows", cookies: owner.all(),
		json: map[string]any{"name": "Todo", "category": "UNSTARTED", "color": "#888888", "position": 1}})
	if err != nil {
		return err
	}
	if res.status != http.StatusOK && res.status != http.StatusCreated {
		return fmt.Errorf("create status: %s", res)
	}
	state2 := res.field("id")

	res, err = p.do(request{method: "POST", path: "/api/v1/workspaces/" + ws + "/agents", cookies: owner.all(),
		json: map[string]any{
			"name": "coder", "teamIds": []string{team1, team2}, "driver": "external",
			"token": map[string]any{"scopes": []string{"work", "issues:write"}, "teamIds": []string{team1}, "ttlHours": 1},
		}})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusCreated); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	agentID, token := res.field("id"), res.field("token")
	if !strings.HasPrefix(token, "conv_agent_") {
		return fmt.Errorf("create agent returned no token: %s", res)
	}
	inLane, _, err := p.createIssue(owner, map[string]string{"teamId": team1, "stateId": state1, "title": "lane", "assigneeId": agentID})
	if err != nil {
		return err
	}
	offLane, _, err := p.createIssue(owner, map[string]string{"teamId": team2, "stateId": state2, "title": "off", "assigneeId": agentID})
	if err != nil {
		return err
	}

	res, err = p.do(request{method: "POST", path: "/api/v1/issues/" + inLane + "/claim", bearer: token})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return fmt.Errorf("claim in lane: %w", err)
	}
	etag := res.header.Get("ETag")
	res, err = p.do(request{method: "POST", path: "/api/v1/issues/" + inLane, bearer: token,
		header: map[string]string{"If-Match": etag}, json: map[string]string{"title": "worked"}})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return fmt.Errorf("write in lane with the claim's ETag: %w", err)
	}

	for _, c := range []struct {
		rq   request
		want int
	}{
		{request{method: "POST", path: "/api/v1/issues/" + offLane + "/claim"}, http.StatusNotFound},
		{request{method: "POST", path: "/api/v1/issues/" + offLane, header: map[string]string{"If-Match": "0"}, json: map[string]string{"title": "x"}}, http.StatusNotFound},
		{request{method: "GET", path: "/api/v1/sync_actions/bootstrap?workspaceId=" + ws}, http.StatusForbidden},
		{request{method: "DELETE", path: "/api/v1/issues/" + inLane}, http.StatusForbidden},
		{request{method: "POST", path: "/api/v1/issue_comments?issueId=" + inLane, json: map[string]string{"body": "x"}}, http.StatusForbidden},
		{request{method: "GET", path: "/api/v1/workspaces/" + ws + "/agents"}, http.StatusForbidden},
		{request{method: "POST", path: "/api/v1/workspaces/" + ws + "/agents", json: map[string]string{"name": "escalate"}}, http.StatusForbidden},
	} {
		c.rq.bearer = token
		res, err := p.do(c.rq)
		if err != nil {
			return fmt.Errorf("%s %s: %w", c.rq.method, c.rq.path, err)
		}
		if res.status != c.want {
			return fmt.Errorf("scoped token %s %s: %s, want %d", c.rq.method, c.rq.path, res, c.want)
		}
	}

	res, err = p.do(request{method: "POST", path: "/api/v1/workspaces/" + ws + "/agents/" + agentID + "/token/revoke",
		cookies: owner.all(), json: map[string]string{}})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	res, err = p.do(request{method: "GET", path: "/api/v1/agent/queue", bearer: token})
	if err != nil {
		return err
	}
	if res.status != http.StatusUnauthorized {
		return fmt.Errorf("revoked token: %s, want 401", res)
	}
	return nil
}

// checkMCPScopes: the MCP endpoint re-dispatches each tool call through the
// production router under the caller's token, so a scoped token keeps its
// lane there too.
func (p *probe) checkMCPScopes() error {
	owner, err := p.signIn()
	if err != nil {
		return err
	}
	ws, team1, state1, err := p.newWorkspace(owner)
	if err != nil {
		return err
	}
	res, err := p.do(request{method: "POST", path: "/api/v1/teams", cookies: owner.all(),
		json: map[string]string{"name": "Other", "identifier": "OTH", "workspaceId": ws}})
	if err != nil {
		return err
	}
	team2 := res.field("id")
	res, err = p.do(request{method: "POST", path: "/api/v1/" + team2 + "/workflows", cookies: owner.all(),
		json: map[string]any{"name": "Todo", "category": "UNSTARTED", "color": "#888888", "position": 1}})
	if err != nil {
		return err
	}
	state2 := res.field("id")
	res, err = p.do(request{method: "POST", path: "/api/v1/workspaces/" + ws + "/agents", cookies: owner.all(),
		json: map[string]any{
			"name": "mcp-coder", "teamIds": []string{team1, team2}, "driver": "external",
			"token": map[string]any{"scopes": []string{"work"}, "teamIds": []string{team1}, "ttlHours": 1},
		}})
	if err != nil {
		return err
	}
	if err := expectStatus(res, http.StatusCreated); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	agentID, token := res.field("id"), res.field("token")
	inLane, _, err := p.createIssue(owner, map[string]string{"teamId": team1, "stateId": state1, "title": "lane", "assigneeId": agentID})
	if err != nil {
		return err
	}
	offLane, _, err := p.createIssue(owner, map[string]string{"teamId": team2, "stateId": state2, "title": "off", "assigneeId": agentID})
	if err != nil {
		return err
	}

	ping := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}
	res, err = p.do(request{method: "POST", path: "/api/v1/mcp", cookies: owner.all(), json: ping})
	if err != nil {
		return err
	}
	if res.status != http.StatusForbidden {
		return fmt.Errorf("MCP with a web session: %s, want 403", res)
	}
	res, err = p.do(request{method: "POST", path: "/api/v1/mcp", bearer: token, json: ping,
		header: map[string]string{"Origin": "https://evil.example"}})
	if err != nil {
		return err
	}
	if res.status != http.StatusForbidden {
		return fmt.Errorf("MCP from a foreign origin: %s, want 403", res)
	}

	for _, c := range []struct {
		tool      string
		args      map[string]any
		wantError bool
		contains  string
	}{
		{"claim_issue", map[string]any{"issueId": inLane}, false, `"claim"`},
		{"update_issue", map[string]any{"issueId": inLane, "version": 0, "title": "x"}, true, `"requiredScope":"issues:write"`},
		{"claim_issue", map[string]any{"issueId": offLane}, true, "HTTP 404"},
		{"claim_issue", map[string]any{"issueId": "../workspaces/" + ws + "/agents"}, true, "issueId must be"},
	} {
		res, err := p.do(request{method: "POST", path: "/api/v1/mcp", bearer: token, json: map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": c.tool, "arguments": c.args},
		}})
		if err != nil {
			return err
		}
		if err := expectStatus(res, http.StatusOK); err != nil {
			return fmt.Errorf("MCP %s: %w", c.tool, err)
		}
		var out struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(res.body, &out); err != nil || len(out.Result.Content) == 0 {
			return fmt.Errorf("MCP %s: unreadable result %s", c.tool, res)
		}
		if out.Result.IsError != c.wantError || !strings.Contains(out.Result.Content[0].Text, c.contains) {
			return fmt.Errorf("MCP %s %v: isError=%v %q, want isError=%v containing %q",
				c.tool, c.args, out.Result.IsError, out.Result.Content[0].Text, c.wantError, c.contains)
		}
	}
	return nil
}

// ---- helpers ---------------------------------------------------------------

// newWorkspace onboards a workspace for the session and returns it with
// its first team and one of that team's statuses.
func (p *probe) newWorkspace(s *session) (ws, teamID, stateID string, err error) {
	res, err := p.do(request{method: "POST", path: "/api/v1/workspaces/onboarding", cookies: s.all(),
		json: map[string]string{"workspaceName": "Probe " + randHex(3), "teamName": "Probe", "teamIdentifier": "PRB"}})
	if err != nil {
		return "", "", "", err
	}
	if err := expectStatus(res, http.StatusOK); err != nil {
		return "", "", "", fmt.Errorf("onboarding: %w", err)
	}
	ws = res.field("id")
	boot, err := p.bootstrap(s, ws, "Team,Workflow")
	if err != nil {
		return "", "", "", err
	}
	for _, r := range boot {
		if r.ModelName == "Team" && teamID == "" {
			teamID, _ = r.Data["id"].(string)
		}
	}
	stateID, err = p.firstState(s, ws, teamID)
	return ws, teamID, stateID, err
}

// firstState returns one workflow status of the team.
func (p *probe) firstState(s *session, ws, teamID string) (string, error) {
	boot, err := p.bootstrap(s, ws, "Workflow")
	if err != nil {
		return "", err
	}
	for _, r := range boot {
		if r.ModelName == "Workflow" && r.Data["teamId"] == teamID {
			if id, _ := r.Data["id"].(string); id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("team %s has no status", teamID)
}

func (p *probe) bootstrap(s *session, ws, models string) ([]syncRecord, error) {
	res, err := p.do(request{method: "GET", cookies: s.all(),
		path: "/api/v1/sync_actions/bootstrap?workspaceId=" + ws + "&modelNames=" + models})
	if err != nil {
		return nil, err
	}
	var boot struct {
		SyncActions []syncRecord `json:"syncActions"`
	}
	if err := json.Unmarshal(res.body, &boot); err != nil {
		return nil, fmt.Errorf("bootstrap: %s", res)
	}
	return boot.SyncActions, nil
}

// createIssue creates an issue as the session and returns its id and
// ETag.
func (p *probe) createIssue(s *session, issue map[string]string) (id, etag string, err error) {
	res, err := p.do(request{method: "POST", path: "/api/v1/issues", cookies: s.all(), json: issue})
	if err != nil {
		return "", "", err
	}
	if err := expectStatus(res, http.StatusCreated); err != nil {
		return "", "", fmt.Errorf("create issue: %w", err)
	}
	return res.field("id"), res.header.Get("ETag"), nil
}

// checkGitHubWebhook: a delivery carries no Origin and no session, like
// GitHub's; its signature is its only credential. Unsigned, wrongly
// signed and tampered deliveries are refused, a signed ping is answered.
func (p *probe) checkGitHubWebhook() error {
	sign := func(secret, body string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	deliver := func(body, signature string) (response, error) {
		header := map[string]string{"Origin": "", "X-GitHub-Event": "ping", "X-Hub-Signature-256": signature}
		return p.do(request{method: "POST", path: "/api/github/webhook", body: body, ctype: "application/json", header: header})
	}
	ping := `{"zen":"Approachable is better than simple.","hook_id":1}`
	for name, attempt := range map[string][2]string{
		"unsigned":      {ping, ""},
		"wrong secret":  {ping, sign(p.webhookSecret+"x", ping)},
		"tampered body": {ping + " ", sign(p.webhookSecret, ping)},
	} {
		res, err := deliver(attempt[0], attempt[1])
		if err != nil {
			return err
		}
		if res.status != http.StatusUnauthorized {
			return fmt.Errorf("%s delivery: %s, want 401", name, res)
		}
	}
	res, err := deliver(ping, sign(p.webhookSecret, ping))
	if err != nil {
		return err
	}
	return expectStatus(res, http.StatusOK)
}

func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// siblingPort is the same scheme and host on another port: same-site,
// but another origin.
func siblingPort(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return evilOrigin
	}
	u.Host = u.Hostname() + ":1"
	return u.Scheme + "://" + u.Host
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
