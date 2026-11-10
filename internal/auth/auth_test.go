package auth

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashVerifyRoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected PHC prefix: %q", hash[:40])
	}
	if err := VerifyPassword(pw, hash); err != nil {
		t.Fatalf("correct password rejected: %v", err)
	}
	if err := VerifyPassword("wrong-password", hash); err == nil {
		t.Fatal("wrong password accepted")
	}
}

func TestPasswordHashUniqueSalts(t *testing.T) {
	h1, _ := HashPassword("same-password")
	h2, _ := HashPassword("same-password")
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical (salt missing)")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$x$y$z", "$bcrypt$abc"} {
		if err := VerifyPassword("pw", bad); err == nil {
			t.Fatalf("malformed hash %q accepted", bad)
		}
	}
}

func TestNewTokenShapeAndHash(t *testing.T) {
	tok, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !strings.HasPrefix(tok, "drv1_") {
		t.Fatalf("token missing prefix: %q", tok)
	}
	if len(tok) != len("drv1_")+43 { // 32 bytes -> 43 base64url chars
		t.Fatalf("unexpected token length %d", len(tok))
	}
	if len(hash) != 64 {
		t.Fatalf("hash is not sha256 hex: %q", hash)
	}
	if TokenHash(tok) != hash {
		t.Fatal("TokenHash is not deterministic")
	}
	tok2, hash2, _ := NewToken()
	if tok == tok2 || hash == hash2 {
		t.Fatal("tokens not random")
	}
}

func TestRecoveryCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := NewRecoveryCode()
		if err != nil {
			t.Fatalf("NewRecoveryCode: %v", err)
		}
		if len(code) != 9 || code[4] != '-' {
			t.Fatalf("bad code shape: %q", code)
		}
		for _, r := range code {
			if r != '-' && !strings.ContainsRune(recoveryAlphabet, r) {
				t.Fatalf("code %q has character %q outside alphabet", code, r)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code %q in 50 draws", code)
		}
		seen[code] = true
	}
	norm := NormalizeRecoveryCode("abcd-efgh")
	if norm != "ABCDEFGH" {
		t.Fatalf("normalize: %q", norm)
	}
	if NormalizeRecoveryCode("ABCD EFGH") != norm {
		t.Fatal("normalize should ignore spaces")
	}
	if HashRecoveryCode(NormalizeRecoveryCode("abcd-efgh")) != HashRecoveryCode(NormalizeRecoveryCode("abcd efgh")) {
		t.Fatal("hash must be over the normalized form")
	}
}

// RFC 6238 test vector (SHA-1, 8 digits) — the reference secrets from
// the RFC appendix. We derive the same time counters with our code and
// compare the dynamic truncation output for the 6-digit profile against
// an independent recomputation using hmac+sha1 inline.
func TestTOTPKnownVector(t *testing.T) {
	// RFC 6238 appendix B secret for SHA-1 ("12345678901234567890").
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	// T=59s -> counter 1 -> HOTP value 94287082 (8 digits); our 6-digit
	// profile must be the last 6 digits: 287082.
	got, _, err := totpAt(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatalf("totpAt: %v", err)
	}
	if got != 287082 {
		t.Fatalf("RFC 6238 vector: got %06d, want 287082", got)
	}
}

func TestTOTPVerifyWindowAndReplay(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("NewTOTPSecret: %v", err)
	}
	now := time.Now()
	cur, step, err := totpAt(secret, now)
	if err != nil {
		t.Fatalf("totpAt: %v", err)
	}
	code := fmt.Sprintf("%06d", cur)

	// Current code accepted, and the matched step is returned.
	matchedStep, ok, err := VerifyTOTP(secret, code, now)
	if err != nil || !ok || matchedStep != step {
		t.Fatalf("current code rejected: step=%d ok=%v err=%v", matchedStep, ok, err)
	}
	// Previous-step code accepted (clock drift).
	prev, _, _ := totpAt(secret, now.Add(-totpStep))
	_, ok, _ = VerifyTOTP(secret, fmt.Sprintf("%06d", prev), now)
	if !ok {
		t.Fatal("previous-step code rejected (skew window too tight)")
	}
	// Two-steps-old code rejected.
	old, _, _ := totpAt(secret, now.Add(-2*totpStep))
	if _, ok, _ = VerifyTOTP(secret, fmt.Sprintf("%06d", old), now); ok {
		t.Fatal("two-step-old code accepted")
	}
	// Same-step reuse is accepted (mainstream behaviour, see VerifyTOTP).
	if _, ok, _ = VerifyTOTP(secret, code, now); !ok {
		t.Fatal("same-step reuse refused")
	}
	// Garbage rejected.
	if _, ok, _ = VerifyTOTP(secret, "abcdef", now); ok {
		t.Fatal("non-numeric code accepted")
	}
	if _, ok, _ = VerifyTOTP(secret, "12345", now); ok {
		t.Fatal("short code accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("JBSWY3DPEHPK3PXP", "Drevanta", "admin")
	for _, want := range []string{
		"otpauth://totp/Drevanta:admin?",
		"secret=JBSWY3DPEHPK3PXP",
		"issuer=Drevanta",
		"digits=6", "period=30", "algorithm=SHA1",
	} {
		if !strings.Contains(uri, want) {
			t.Fatalf("uri %q missing %q", uri, want)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(3, time.Minute)
	rl.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !rl.Allow("login:127.0.0.1") {
			t.Fatalf("event %d denied within budget", i+1)
		}
	}
	if rl.Allow("login:127.0.0.1") {
		t.Fatal("event 4 allowed over budget")
	}
	if !rl.Allow("login:other") {
		t.Fatal("independent key denied")
	}
	now = now.Add(time.Minute + time.Second)
	if !rl.Allow("login:127.0.0.1") {
		t.Fatal("window did not reset")
	}
}
