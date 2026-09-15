package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewCode(t *testing.T) {
	codes := map[string]bool{}
	for i := 0; i < 100; i++ {
		c, err := newCode()
		if err != nil {
			t.Fatalf("newCode: %v", err)
		}
		if len(c) != 20 {
			t.Fatalf("code length = %d, want 20: %q", len(c), c)
		}
		if c != strings.ToUpper(c) {
			t.Fatalf("code not uppercased: %q", c)
		}
		for _, r := range c {
			if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_", r) {
				t.Fatalf("code %q has non-URL-safe rune %q (it travels in a URL hash fragment)", c, r)
			}
		}
		codes[c] = true
	}
	if len(codes) < 95 { // 120 bits of entropy: collisions are effectively impossible
		t.Fatalf("only %d unique codes out of 100", len(codes))
	}
}

func TestNewID(t *testing.T) {
	id := newID()
	if len(id) != 40 { // 20 bytes, hex
		t.Fatalf("id length = %d, want 40: %q", len(id), id)
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("id %q has non-hex rune %q", id, r)
		}
	}
	if newID() == id {
		t.Fatal("two newID calls produced the same id")
	}
}

func TestHashValue(t *testing.T) {
	// The stored hash is the sha256 hex of the raw value: pin the vector
	// so a hash-function swap (e.g. to blake2) fails loudly.
	sum := sha256.Sum256([]byte("s3cret"))
	want := hex.EncodeToString(sum[:])
	if got := hashValue("s3cret"); got != want {
		t.Fatalf("hashValue = %q, want %q", got, want)
	}
	if hashValue("s3cret") == hashValue("s3creT") {
		t.Fatal("hash is case-insensitive; codes are case-sensitive at issue time")
	}
}

func TestCodeRequestEmail(t *testing.T) {
	// Top-level field wins and is normalized.
	var r codeRequest
	r.Email = "  User@Example.COM "
	if got := r.email(); got != "user@example.com" {
		t.Fatalf("email() = %q, want normalized", got)
	}
	// Legacy contactInfo shape is the fallback.
	var r2 codeRequest
	r2.ContactInfo.Email = "legacy@example.com"
	if got := r2.email(); got != "legacy@example.com" {
		t.Fatalf("email() = %q, want legacy fallback", got)
	}
	// Empty stays empty.
	var r3 codeRequest
	if got := r3.email(); got != "" {
		t.Fatalf("email() = %q, want empty", got)
	}
}

func TestEmailPattern(t *testing.T) {
	valid := []string{
		"a@b.co",
		"user.name+tag@example.org",
		"first-last@sub.domain.io",
	}
	invalid := []string{
		"",
		"@",
		"@b.co",
		"a@",
		"a@b",                                // no dot in the domain
		"a@@b.co",                            // double at
		"a@b@c.co",                           // second at in the domain
		"ab.co",                              // no at at all
		"a@b.co@" + strings.Repeat("x", 250), // > 255 total
		"a@" + strings.Repeat("x", 251) + ".co",
	}
	for _, email := range valid {
		if !emailPattern(email) {
			t.Fatalf("%q rejected, want accepted", email)
		}
	}
	for _, email := range invalid {
		if emailPattern(email) {
			t.Fatalf("%q accepted, want rejected", email)
		}
	}
}
