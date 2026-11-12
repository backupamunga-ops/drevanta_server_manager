# Authentication & Onboarding — the complete user guide

This manual walks through every screen of the Drevanta server console:
first-time setup, multi-factor authentication, recovery codes, sign-in
(desktop and mobile), the three ways to recover a locked account, and
day-to-day account management. Every step shows the success state and
the failure states you can meet along the way.

Screenshots were taken from v0.4.18. Desktop shots are 1440×900,
mobile shots 390×844 (the console is fully responsive).

---

## Contents

1. [First-time setup — the claim link](#1-first-time-setup--the-claim-link)
2. [Create the admin account](#2-create-the-admin-account)
3. [Enable multi-factor authentication](#3-enable-multi-factor-authentication)
4. [Save the recovery codes](#4-save-the-recovery-codes)
5. [Choose the address — server IP or your domain](#5-choose-the-address--server-ip-or-your-domain)
6. [You're all set — the completion checklist](#6-youre-all-set--the-completion-checklist)
7. [Signing in — two steps](#7-signing-in--two-steps)
8. [Sign-in failure states](#8-sign-in-failure-states)
9. [Locked out? Three ways back in](#9-locked-out-three-ways-back-in)
10. [Managing the account — Settings](#10-managing-the-account--settings)
11. [Security — what is enforced](#11-security--what-is-enforced)
12. [Troubleshooting](#12-troubleshooting)

---

## 1. First-time setup — the claim link

After installation the daemon prints a **one-time setup link**:
`https://<your-server>/onboarding#token=drv1_…`. Open it in a browser.
The link works exactly once, expires after 30 minutes, and only while
no administrator exists.

![Claim screen](images/desk-12-onboarding-claim.png)

![Claim screen — mobile](images/mob-12-onboarding-claim.png)

*Desktop — claim screen with the token detected from the pasted link.*

You can also enter a token manually: open `/onboarding` without the
link and paste the `drv1_…` token.

**Failure states**

| Symptom | Meaning | Fix |
|---|---|---|
| "token expired" | more than 30 minutes passed | run `sudo drevanta claim regenerate` on the server |
| "token used" | somebody already finished setup | sign in instead |
| "token invalid" | the token was rotated or mistyped | copy the whole link again |

The token lives only in the browser tab until you submit — it is never
stored in cookies, localStorage or logs.

---

## 2. Create the admin account

Pick a **username** (3–32 characters), an **email**, and a **password**
(12–128 characters). The email is a second login identifier — you can
sign in with the username *or* the email — and the target for
password-reset links later. The strength meter reacts live; a Caps-Lock
warning appears while typing.

**Failure states**: too-short password ("Needs N more characters"),
mismatched confirmation ("Passwords do not match yet."), a username
with invalid characters, or a missing/invalid email. Nothing is sent
to the server until every field passes — the first invalid field is
focused for you.

---

## 3. Enable multi-factor authentication

The console asks for an authenticator app (Aegis, 1Password, Google
Authenticator, …). Scan the QR code — or reveal the setup key and enter
it manually:

![MFA enrolment](images/desk-13-onboarding-mfa-collapsed.png)

![MFA enrolment — mobile](images/mob-13-onboarding-mfa-collapsed.png)

*The setup key is collapsed by default. "Reveal" shows the whole field;*

![Secret revealed](images/desk-14-onboarding-mfa-revealed.png)

![Secret revealed — mobile](images/mob-14-onboarding-mfa-revealed.png)

*"Hide" collapses it again. "How does this work?" opens step-by-step
instructions below the QR code.*

Enter the current 6-digit code and confirm.

**Failure state**: "invalid authenticator code" — usually a code that
just rolled over, or device/server clock drift. Wait for the next code
and try again; check NTP on the server if it keeps failing.

MFA can be skipped ("Skip for now") with an explicit risk
acknowledgement — the account then protects by password only, and MFA
can be enabled later under Settings → Account security.

---

## 4. Save the recovery codes

After verification the console shows **10 single-use recovery codes**.
They are shown exactly once; you must confirm they are stored before
continuing. Each code signs you in once when the normal path fails
(see [section 9](#9-locked-out-three-ways-back-in)). Regenerating the
set later invalidates all previous codes.

![Recovery codes](images/desk-15-onboarding-recovery-codes.png)

![Recovery codes — mobile](images/mob-15-onboarding-recovery-codes.png)

*Store them offline — a password manager or printed paper, not a
screenshot in the same cloud as the server.*

---

## 5. Choose the address — server IP or your domain

The console is reachable over the server IP with a publicly trusted
certificate, or under your own domain. "I have a domain" → "Set up
your domain" asks for the hostname, shows the exact DNS records to
create, and verifies before switching anything:

![Address choice](images/desk-16-onboarding-address.png)

![Address choice — mobile](images/mob-16-onboarding-address.png)

**Failure state** — "Domain not connected yet": the DNS records were
not found or do not point at this server yet. The panel shows both
records with Copy buttons; DNS changes can take minutes to hours. The
server-IP address keeps working the whole time, and you can skip:

![Domain failure](images/desk-17-onboarding-domain-failure.png)

![Domain failure — mobile](images/mob-17-onboarding-domain-failure.png)

The certificate is issued only after DNS verifies, and any failure
leaves the IP address untouched (fail-safe by design).

---

## 6. You're all set — the completion checklist

The final screen summarises what was configured (admin credentials, MFA,
account secured, address/domain) with live CPU, memory and disk
readings, and links back to the domain configuration if you skipped it:

![Completion checklist](images/desk-18-onboarding-done-checklist.png)

![Completion checklist — mobile](images/mob-18-onboarding-done-checklist.png)

"Proceed to dashboard" signs you in. A paused onboarding (browser
closed mid-way) resumes where you left off when you sign in again.

---

## 7. Signing in — two steps

Sign-in works with **username or email** plus password:

![Sign-in step 1](images/desk-01-login-step1.png)

![Sign-in step 1 — mobile](images/mob-01-login-step1.png)

*The password field has a Show/Hide toggle; masked by default.*

![Password revealed](images/desk-02-login-password-revealed.png)

![Password revealed — mobile](images/mob-02-login-password-revealed.png)

When MFA is enabled the console switches to its own **Two-step
verification** screen — the code field only, autofocused:

![Two-step verification](images/desk-04-login-mfa-screen.png)

![Mobile two-step verification](images/mob-04-login-mfa-screen.png)

![Desktop — the two-step screen survives a reload](images/desk-05-login-mfa-after-reload.png)

![Two-step screen after reload — mobile](images/mob-05-login-mfa-after-reload.png)

*Desktop — after a full page reload the console is still on the code
step; the password is never asked again.*

*Mobile — switching to your authenticator app and back is safe: the
screen survives the page reload that phones often trigger, so you never
re-enter the password.*

The code field accepts the 6-digit code **or** one of the recovery
codes. After the code verifies, the dashboard opens.

---

## 8. Sign-in failure states

| Screen | What happened | What to do |
|---|---|---|
| "invalid username or password" | wrong credentials at step 1 | retry; the username/email must match exactly |
| "invalid authenticator code" | wrong 6-digit code | wait for the next code |
| "too many attempts, slow down" | more than 10 sign-in attempts per minute from your address | wait a minute |
| "too many failed attempts for this account — wait a few minutes" | 10 failed attempts against the account in 15 minutes (from any address) | wait 5 minutes; then recovery paths below |
| "this sign-in attempt expired" | the two-step challenge sat for 5 minutes unused | sign in again |

![Wrong password](images/desk-03-login-wrong-password.png)


![Wrong password — mobile](images/mob-03-login-wrong-password.png)

![Wrong code](images/desk-06-login-mfa-wrong-code.png)


![Wrong code — mobile](images/mob-06-login-mfa-wrong-code.png)

---

## 9. Locked out? Three ways back in

### Path B — recovery-code sign-in (no password needed)

"Use a recovery code instead" on the sign-in screen: username/email +
one of your saved codes. The code replaces both factors for this one
sign-in and lands on a forced "Set a new password" screen — after
saving, you are on the dashboard with a fresh password.

![Recovery-code sign-in](images/desk-07-login-recovery-code.png)

![Recovery-code sign-in — mobile](images/mob-07-login-recovery-code.png)

### Path A — email reset link (needs SMTP configured)

"Forgot password?" asks for your username or email. If the account has
an email and mail delivery is configured (Settings → Email delivery),
a **single-use link** valid for 30 minutes is sent:

![Forgot password](images/desk-08-login-forgot.png)

![Forgot password — mobile](images/mob-08-login-forgot.png)

The response is the same whatever you type — nobody can probe whether
an account exists. Opening the link shows the reset screen; completing
it signs out every existing session.

![Reset screen](images/desk-09-login-reset-link.png)

![Reset screen — mobile](images/mob-09-login-reset-link.png)

### Path C — break-glass CLI (root on the server)

If email is down and the codes are lost, root access always wins:

```
sudo drevantad admin reset-password            # prompts twice, input hidden
sudo drevantad admin reset-password --username admin --password '…'
```

It validates the password policy, ends all sessions and writes an
audit event.

### Last resort

Lost password, codes, email **and** SSH access cannot be recovered by
design — reinstall and re-claim (`sudo drevanta claim regenerate`
works only before the first owner exists). This is the trade-off of a
self-hosted, single-admin console.

---

## 10. Managing the account — Settings

![Settings — account, mail delivery, security](images/desk-11-settings-account-smtp.png)

![Settings — mobile](images/mob-11-settings-account-smtp.png)

*Settings: Account (email + password), Email delivery (SMTP), Account
security, Access address.*

- **Email** — change or clear it; sign-in falls back to username-only
  when cleared. Uniqueness is enforced case-insensitively.
- **Change password** — requires the current password; every OTHER
  device is signed out (the count is shown).
- **Sign out everywhere** — ends all sessions including this one.
- **Email delivery (SMTP)** — host, port (implicit TLS on 465, STARTTLS
  otherwise), credentials and From address, plus a "Send test message"
  button that reports sender errors verbatim. The password is stored
  server-side and never displayed again.
- **Account security** — enable MFA later or regenerate recovery codes.
- **Access address** — add/remove the custom domain; warns when the
  certificate has ≤ 14 days left.

![Dashboard](images/desk-10-dashboard.png)

![Mobile dashboard](images/mob-10-dashboard.png)

---

## 11. Security — what is enforced

**Credentials and sessions**

- Argon2id password hashing (memory-hard), 12–128 character policy.
- Two-step sign-in: the password is verified before any code is asked.
- A stolen "sign-in attempt" token grants nothing — it is single-use,
  expires in 5 minutes, allows at most 5 wrong codes and is stored
  hashed, in memory only.
- Session cookies: HttpOnly, SameSite=Lax, `Secure` on HTTPS, random
  256-bit tokens stored hashed; sliding 24-hour expiry with a hard
  7-day cap; ended on logout, password change and reset.

**Abuse resistance**

- Per-address rate limits: login and recovery 10/min (tunable via
  `DREVANTA_RATE_LIMIT`), separate buckets for claim/MFA endpoints.
- Per-account lockout: 10 failures in 15 minutes → 5-minute lockout,
  independent of source address.
- Constant "invalid username or password" responses — no account
  enumeration on login or password reset.
- Bounded Argon2 concurrency (memory-exhaustion guard) and 16 KiB JSON
  body caps; the HTTP servers run read/write/idle timeouts.

**Transport and browser hardening**

- Public HTTPS with automatic Let's Encrypt certificates (renewal
  monitored; console warning at ≤ 14 days); the backend stays
  loopback-only.
- Content-Security-Policy pinned to 'self', `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`, Permissions-Policy (camera, mic,
  geolocation, payment off), `X-Content-Type-Options: nosniff`, HSTS.
- Same-origin checks on every state-changing request.
- HTML shells are `no-store` — stale bundles cannot mask an update.

**Data**

- All SQL uses parameterised queries (no string-built SQL); SQLite in
  WAL mode with foreign keys on.
- Claim tokens, session cookies, recovery codes and password-reset
  tokens exist only as SHA-256 hashes in the database.
- An audit log records logins (with reasons), MFA changes, recovery-code
  use, email/password changes, resets and lockouts.
- Nightly `VACUUM INTO` snapshots of the database, last 7 kept in
  `/var/lib/drevanta/backups`.

**What the operator should do (server-side)**

- Keep SSH key-only with fail2ban; apply unattended security updates.
- Ensure NTP is active (TOTP depends on the clock).
- Copy the nightly backup off the box; test the restore path:
  stop units → copy snapshot over `/var/lib/drevanta/drevanta.db` →
  start units.
- Configure SPF/DKIM/PTR for the mail domain so reset links arrive.

---

## 12. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| Codes always rejected | server clock drift | enable NTP/chrony; "check the clock" is also shown in the UI |
| Reset mail never arrives | SMTP misconfigured or spam-foldered | Settings → Email delivery → "Send test message"; check SPF/DKIM |
| "Domain not connected yet" for hours | DNS not propagated or wrong records | re-check the two records (Host / Points to); TTL up to hours |
| Locked out, no email, no codes | — | Path C CLI reset from SSH |
| Old UI after an update | browser cache | hard refresh (Ctrl+Shift+R); shells are no-store since 0.4.11 |
| Certificate warning in Settings ≤ 14 days | renewal pending/failing | watch `sudo drevanta access status`; renewal is automatic |

---

*Manual generated for Drevanta v0.4.18. Screenshots live in
`docs/images/` — desktop (`desk-…`) and mobile (`mob-…`).*