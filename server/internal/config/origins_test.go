package config

import "testing"

func TestTrustedRequestOriginsDevLoopback(t *testing.T) {
	list := TrustedRequestOrigins(Config{
		DevMode:   true,
		WebOrigin: "http://176.123.167.143:3000",
		PublicURL: "http://176.123.167.143:3001",
	})
	seen := map[string]bool{}
	for _, o := range list {
		seen[o] = true
	}
	for _, want := range []string{
		"http://localhost:3000",
		"http://127.0.0.1:3000",
		"http://[::1]:3000",
	} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, list)
		}
	}
}
