// authprobe is a black-box auth pentest checklist for a running Converge
// API: the Phase 0 exit criterion ("an internet-facing single-tenant
// deploy survives a basic auth pentest checklist"), made repeatable.
//
// The probe plays three parts. It is the browser, and the hostile page
// that tries to ride one. It is the reverse proxy in front of the API: it
// sends X-Forwarded-For, so the API must trust the probe's address as a
// proxy (the default trusts loopback). And it is the mail server: point
// the API's CONVERGE_SMTP_HOST/PORT at -smtp with CONVERGE_SMTP_TLS=off,
// and sign-in links arrive at the probe the way they reach an inbox.
//
//	go run ./tools/authprobe -api http://127.0.0.1:3101 \
//	    -web-origin http://localhost:3200 -smtp 127.0.0.1:2526
//
// run.sh boots a production-configured server and runs the probe against
// it. Every check prints PASS or FAIL; the exit status is 1 when any
// check fails.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	api := flag.String("api", "http://127.0.0.1:3101", "API base URL")
	webOrigin := flag.String("web-origin", "http://localhost:3200", "the API's CONVERGE_WEB_ORIGIN")
	smtpAddr := flag.String("smtp", "127.0.0.1:2526", "address for the probe's SMTP sink")
	secure := flag.Bool("secure-cookies", false, "expect Secure session cookies (an https web origin)")
	only := flag.String("only", "", "run only checks whose id has this prefix")
	flag.Parse()

	ln, err := net.Listen("tcp", *smtpAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "authprobe: smtp listen: %v\n", err)
		os.Exit(2)
	}
	mail := newMailbox()
	go mail.serve(ln)

	p := &probe{
		api:           strings.TrimRight(*api, "/"),
		webOrigin:     strings.TrimRight(*webOrigin, "/"),
		secureCookies: *secure,
		mail:          mail,
		http: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		only: *only,
	}
	failed := p.run()
	_ = ln.Close()
	if failed > 0 {
		os.Exit(1)
	}
}

type probe struct {
	api, webOrigin string
	secureCookies  bool
	mail           *mailbox
	http           *http.Client
	only           string

	clientIP string // the current check's client, as the proxy reports it
	passed   int
	failed   int
}

// check runs one checklist item from a fresh client address, so one
// item's requests never spend another's rate-limit budget.
func (p *probe) check(id, name string, fn func() error) {
	if p.only != "" && !strings.HasPrefix(id, p.only) {
		return
	}
	p.clientIP = randomIP()
	if err := fn(); err != nil {
		p.failed++
		fmt.Printf("FAIL  %-9s %s\n          %v\n", id, name, err)
		return
	}
	p.passed++
	fmt.Printf("PASS  %-9s %s\n", id, name)
}

// ---- HTTP ------------------------------------------------------------------

type request struct {
	method, path string
	json         any    // sent as application/json
	body         string // sent verbatim with ctype
	ctype        string
	header       map[string]string // "" deletes a default header
	cookies      []*http.Cookie
	bearer       string
	xff          string // default: the check's client address
	allow429     bool   // the check expects throttling: no retry
}

type response struct {
	status  int
	header  http.Header
	body    []byte
	cookies []*http.Cookie
}

func (r response) String() string {
	b := string(r.body)
	if len(b) > 300 {
		b = b[:300] + "..."
	}
	return fmt.Sprintf("HTTP %d %s", r.status, strings.TrimSpace(b))
}

// field reads a top-level string field of a JSON body ("" when absent).
func (r response) field(name string) string {
	var m map[string]any
	if json.Unmarshal(r.body, &m) != nil {
		return ""
	}
	s, _ := m[name].(string)
	return s
}

func (r response) cookie(name string) *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// do sends one request. Unsafe methods carry the web origin, as a
// browser on the app would, unless the request overrides Origin. An
// unexpected 429 waits out Retry-After and retries: against a deployment
// behind a real proxy every check shares one client address.
func (p *probe) do(rq request) (response, error) {
	for attempt := 0; ; attempt++ {
		var body io.Reader
		ctype := rq.ctype
		switch {
		case rq.json != nil:
			raw, err := json.Marshal(rq.json)
			if err != nil {
				return response{}, err
			}
			body, ctype = bytes.NewReader(raw), "application/json"
		case rq.body != "":
			body = strings.NewReader(rq.body)
		}
		req, err := http.NewRequest(rq.method, p.api+rq.path, body)
		if err != nil {
			return response{}, err
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if rq.method != http.MethodGet && rq.method != http.MethodHead && rq.method != http.MethodOptions {
			req.Header.Set("Origin", p.webOrigin)
		}
		xff := rq.xff
		if xff == "" {
			xff = p.clientIP
		}
		req.Header.Set("X-Forwarded-For", xff)
		if rq.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+rq.bearer)
		}
		for k, v := range rq.header {
			if v == "" {
				req.Header.Del(k)
			} else {
				req.Header.Set(k, v)
			}
		}
		for _, c := range rq.cookies {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
		res, err := p.http.Do(req)
		if err != nil {
			return response{}, err
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		out := response{status: res.StatusCode, header: res.Header, body: raw, cookies: res.Cookies()}
		if out.status == http.StatusTooManyRequests && !rq.allow429 && attempt < 30 {
			wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
			time.Sleep(time.Duration(min(max(wait, 1), 5)) * time.Second)
			continue
		}
		return out, nil
	}
}

// ---- sessions --------------------------------------------------------------

// session is a signed-in browser's cookie jar.
type session struct {
	email     string
	accountID string
	jar       map[string]*http.Cookie
}

const (
	cookieAccess   = "st-access-token"
	cookieRefresh  = "st-refresh-token"
	cookieAntiCsrf = "sAntiCsrf"
	cookieFront    = "sFrontToken"
)

// absorb applies Set-Cookie headers the way a browser would.
func (s *session) absorb(cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.MaxAge < 0 || c.Value == "" {
			delete(s.jar, c.Name)
			continue
		}
		s.jar[c.Name] = c
	}
}

func (s *session) cookies(names ...string) []*http.Cookie {
	var out []*http.Cookie
	for _, n := range names {
		if c, ok := s.jar[n]; ok {
			out = append(out, c)
		}
	}
	return out
}

func (s *session) all() []*http.Cookie {
	return s.cookies(cookieAccess, cookieRefresh, cookieAntiCsrf, cookieFront)
}

// inner unwraps the token inside a session cookie envelope.
func (s *session) inner(name string) string {
	c, ok := s.jar[name]
	if !ok {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return ""
	}
	var env struct {
		T string `json:"t"`
	}
	_ = json.Unmarshal(raw, &env)
	return env.T
}

func (s *session) antiCsrf() string {
	if c, ok := s.jar[cookieAntiCsrf]; ok {
		return c.Value
	}
	return ""
}

// requestCode asks for a sign-in link for email and returns the link
// from the mail it produces.
func (p *probe) requestCode(email string) (signInLink, error) {
	before := p.mail.count(email)
	res, err := p.do(request{method: "POST", path: "/api/auth/signinup/code", json: map[string]string{"email": email}})
	if err != nil {
		return signInLink{}, err
	}
	if res.status != http.StatusOK || res.field("status") != "OK" {
		return signInLink{}, fmt.Errorf("create code: %s", res)
	}
	msg, err := p.mail.wait(email, before+1, 10*time.Second)
	if err != nil {
		return signInLink{}, err
	}
	return parseLink(msg)
}

// consume redeems a link as the app's verify page does.
func (p *probe) consume(l signInLink) (response, error) {
	return p.do(request{method: "POST", path: "/api/auth/signinup/code/consume",
		json: map[string]string{"linkCode": l.code, "preAuthSessionId": l.preAuthSessionID}})
}

// signIn runs the full magic-link flow for a fresh address.
func (p *probe) signIn() (*session, error) {
	email := newEmail()
	link, err := p.requestCode(email)
	if err != nil {
		return nil, err
	}
	res, err := p.consume(link)
	if err != nil {
		return nil, err
	}
	if res.status != http.StatusOK || res.field("status") != "OK" {
		return nil, fmt.Errorf("consume: %s", res)
	}
	s := &session{email: email, jar: map[string]*http.Cookie{}}
	s.absorb(res.cookies)
	if s.inner(cookieAccess) == "" || s.inner(cookieRefresh) == "" {
		return nil, fmt.Errorf("sign-in set no session cookies: %v", res.cookies)
	}
	var body struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(res.body, &body)
	s.accountID = body.User.ID
	return s, nil
}

// refresh rotates the session through the cookie flow.
func (p *probe) refresh(s *session) (response, error) {
	return p.do(request{method: "POST", path: "/api/auth/session/refresh",
		cookies: s.cookies(cookieRefresh), header: map[string]string{"anti-csrf": s.antiCsrf()}})
}

// me asks who the bearer of the given cookies or token is.
func (p *probe) me(cookies []*http.Cookie, bearer string) (int, error) {
	res, err := p.do(request{method: "GET", path: "/api/v1/users", cookies: cookies, bearer: bearer})
	return res.status, err
}

// ---- helpers ---------------------------------------------------------------

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newEmail() string { return "probe-" + randHex(6) + "@probe.test" }

// randomIP is an address in 198.18.0.0/15 (RFC 2544 benchmarking): public
// to the API's proxy trust, routable nowhere.
func randomIP() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("198.%d.%d.%d", 18+int(b[0]&1), b[1], max(int(b[2]), 1))
}

func expectStatus(res response, want int) error {
	if res.status != want {
		return fmt.Errorf("want HTTP %d, got %s", want, res)
	}
	return nil
}

func (p *probe) run() int {
	fmt.Printf("authprobe: %s (web origin %s)\n\n", p.api, p.webOrigin)
	p.runChecks()
	fmt.Printf("\n%d passed, %d failed\n", p.passed, p.failed)
	return p.failed
}
