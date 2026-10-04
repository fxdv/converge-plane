package config

import "testing"

func TestTrustedRequestOriginsExtra(t *testing.T) {
	list := TrustedRequestOrigins(Config{
		WebOrigin:      "https://app.example.com",
		PublicURL:      "https://api.example.com",
		WebOriginExtra: []string{"http://localhost:3000"},
	})
	seen := map[string]bool{}
	for _, o := range list {
		seen[o] = true
	}
	if !seen["http://localhost:3000"] || !seen["http://127.0.0.1:3000"] {
		t.Fatalf("expected loopback aliases for extra origin, got %v", list)
	}
}
