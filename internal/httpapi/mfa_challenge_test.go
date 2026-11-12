package httpapi

// Tests for the two-step sign-in challenges (v0.4.15): issuance on
// totp_required, completion via /api/v1/auth/login/mfa, single-use on
// success, attempt cap, TTL expiry, and the no-challenge path for
// accounts without MFA.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// newClaimToken mints an unused claim token directly in the store (the
// way the installer CLI does).
func newClaimToken(t *testing.T, st *store.Store) (string, error) {
	t.Helper()
	token, _, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if _, err := st.CreateClaimToken(context.Background(), auth.TokenHash(token), time.Hour); err != nil {
		return "", err
	}
	return token, nil
}

// enrollAdminMFA runs claim -> MFA setup -> verify and returns the TOTP
// secret plus the recovery codes, leaving the instance active.
func enrollAdminMFA(t *testing.T, h http.Handler, st *store.Store) (secret string, recovery []string) {
	t.Helper()
	token, err := newClaimToken(t, st)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		`{"token":"`+token+`","username":"admin","email":"admin@example.com","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	sess := cookieOf(rec)
	sessHdr := map[string]string{"Cookie": sess.Name + "=" + sess.Value}

	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa setup = %d: %s", rec.Code, rec.Body.String())
	}
	secret = decodeMap(t, rec)["secret"].(string)

	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/verify",
		fmt.Sprintf(`{"code":%q}`, currentTOTP(t, secret)), sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa verify = %d: %s", rec.Code, rec.Body.String())
	}
	codesAny := decodeMap(t, rec)["recovery_codes"].([]any)
	for _, c := range codesAny {
		recovery = append(recovery, c.(string))
	}
	return secret, recovery
}

func loginChallenge(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_required" {
		t.Fatalf("login without code: %d %v", rec.Code, decodeMap(t, rec))
	}
	return decodeMap(t, rec)
}

func TestLoginMFAChallengeFlow(t *testing.T) {
	h, st := newTestHandler(t)
	secret, _ := enrollAdminMFA(t, h, st)

	// Step 1: password verified -> challenge issued with the username.
	m := loginChallenge(t, h)
	challenge, _ := m["mfa_challenge"].(string)
	if challenge == "" {
		t.Fatalf("no mfa_challenge in response: %v", m)
	}
	if m["username"] != "admin" {
		t.Fatalf("challenge response should carry the username: %v", m)
	}

	// A wrong code burns an attempt but keeps the challenge alive.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		`{"challenge":"`+challenge+`","code":"000000"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_invalid" {
		t.Fatalf("wrong code: %d %v", rec.Code, decodeMap(t, rec))
	}

	// The right code completes the sign-in and issues a session.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge, currentTOTP(t, secret)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa login = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["scope"] != "full" {
		t.Fatalf("scope = %v", decodeMap(t, rec))
	}
	sess := cookieOf(rec)
	if sess == nil {
		t.Fatal("no session cookie after mfa login")
	}
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "",
		map[string]string{"Cookie": sess.Name + "=" + sess.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics after mfa login = %d", rec.Code)
	}

	// Single-use: the consumed challenge cannot start a second session.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge, currentTOTP(t, secret)), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "challenge_invalid" {
		t.Fatalf("challenge reuse: %d %v", rec.Code, decodeMap(t, rec))
	}
}

func TestMFAChallengeAttemptCap(t *testing.T) {
	h, st := newTestHandler(t)
	secret, _ := enrollAdminMFA(t, h, st)

	challenge := loginChallenge(t, h)["mfa_challenge"].(string)

	// Attempts 1-4: ordinary wrong-code rejections.
	for i := 1; i <= 4; i++ {
		rec := do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
			`{"challenge":"`+challenge+`","code":"000000"}`, nil)
		if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_invalid" {
			t.Fatalf("attempt %d: %d %v", i, rec.Code, decodeMap(t, rec))
		}
	}

	// Attempt 5 exhausts the challenge.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		`{"challenge":"`+challenge+`","code":"000000"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "challenge_exhausted" {
		t.Fatalf("attempt 5: %d %v", rec.Code, decodeMap(t, rec))
	}

	// Dropped: even the CORRECT code no longer works on this challenge.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge, currentTOTP(t, secret)), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "challenge_invalid" {
		t.Fatalf("post-exhaustion: %d %v", rec.Code, decodeMap(t, rec))
	}
}

func TestMFAChallengeBadRequests(t *testing.T) {
	h, st := newTestHandler(t)
	enrollAdminMFA(t, h, st)

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		`{"challenge":"nonsense","code":"123456"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "challenge_invalid" {
		t.Fatalf("unknown challenge: %d %v", rec.Code, decodeMap(t, rec))
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login/mfa", `{"challenge":""}`, nil)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "invalid_input" {
		t.Fatalf("empty fields: %d %v", rec.Code, decodeMap(t, rec))
	}
	// Unknown fields are rejected (strict decode), no 500s.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login/mfa",
		`{"challenge":"x","code":"123456","extra":1}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
}

func TestLoginWithoutMFANeedsNoChallenge(t *testing.T) {
	h, st := newTestHandler(t)
	// Claim + skip MFA (mfa_mode=skipped): password alone must sign in.
	token, err := newClaimToken(t, st)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		`{"token":"`+token+`","username":"admin","email":"admin@example.com","password":"correct horse battery"}`, nil)
	sess := cookieOf(rec)
	sessHdr := map[string]string{"Cookie": sess.Name + "=" + sess.Value}
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip",
		`{"acknowledge_risk":true}`, sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}
	do(t, h, http.MethodPost, "/api/v1/auth/logout", "", sessHdr)

	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("password-only login = %d: %s", rec.Code, rec.Body.String())
	}
	if m := decodeMap(t, rec); m["scope"] != "full" {
		t.Fatalf("scope = %v", m)
	}
}

func TestMFAChallengeStoreExpiry(t *testing.T) {
	m := newMFAChallengeStore()
	now := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return now }
	m.ttl = 30 * time.Second

	token, err := m.issue(1, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.peek(token); !ok {
		t.Fatal("fresh challenge should resolve")
	}
	now = now.Add(31 * time.Second)
	if _, ok := m.peek(token); ok {
		t.Fatal("expired challenge must not resolve")
	}
	// Counting an attempt on an expired/unknown challenge reports
	// exhausted (the next peek would fail anyway).
	if !m.countAttempt(token) {
		t.Fatal("attempt on unknown challenge should report exhausted")
	}
}

func TestMFAChallengeStoreSweepAndCap(t *testing.T) {
	m := newMFAChallengeStore()
	now := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return now }

	// Fill to the cap with stale challenges, then verify the sweep frees
	// room on the next issue.
	for i := 0; i < mfaChallengeMaxLive; i++ {
		if _, err := m.issue(int64(i), "u"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.issue(1, "admin"); err != errChallengeFull {
		t.Fatalf("over-cap issue = %v, want errChallengeFull", err)
	}
	now = now.Add(mfaChallengeTTL + time.Minute)
	tok, err := m.issue(1, "admin")
	if err != nil {
		t.Fatalf("issue after sweep = %v", err)
	}
	if _, ok := m.peek(tok); !ok {
		t.Fatal("fresh challenge after sweep should resolve")
	}
	if len(m.live) != 1 {
		t.Fatalf("live = %d, want 1 after sweep", len(m.live))
	}
}
