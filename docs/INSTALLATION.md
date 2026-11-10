# Installing Drevanta — the complete first-time guide

This guide is the actual customer journey: **buy a VPS → SSH into it →
run one official installation command → open the setup link in your
browser → create the administrator → configure MFA → save recovery
codes → enter the dashboard.**

Nothing in the normal journey requires a custom domain, a second
terminal session, manual file transfers, Git, or a compiler.

---

## The journey at a glance

| # | Step | Where |
| - | ---- | ----- |
| 1 | Buy a VPS (Ubuntu/Debian, x86_64, ≥ 512 MB RAM) | your hosting provider |
| 2 | SSH into the VPS | your computer |
| 3 | Run the official Drevanta installation command | the VPS |
| 4 | Open the HTTPS setup link (or the SSH fallback) | your computer |
| 5 | Create the administrator account | browser |
| 6 | Set up MFA (recommended, can be skipped for now) | browser + authenticator app |
| 7 | Save recovery codes (when MFA is enabled) | browser + password manager |
| 8 | Choose the access address (server IP, or your own domain) | browser |
| 9 | Enter the dashboard | browser |

## Before you start: what kind of server you need

- **Operating system:** Ubuntu 20.04+ or Debian 11+ (other systemd
  distributions usually work but are not the tested target).
- **Architecture:** linux/amd64 (x86_64).
- **Resources:** 512 MB RAM class or better; about 100 MB of disk.
- **You need:** the VPS IP address and SSH credentials (user + password
  or key) — exactly what your hosting provider emailed you.

## Step 1 — Connect to your VPS

From your own computer:

```bash
ssh root@YOUR_VPS_IP
```

The username may differ from `root` depending on the provider (for
example `ubuntu`, `admin`, or `debian`). Windows users: open PowerShell
(`ssh` is built into Windows 10/11).

## Step 2 — Run the official installation command

Still inside the VPS SSH session, run:

```bash
curl -fsSL https://github.com/backupamunga-ops/drevanta_server_manager/releases/latest/download/bootstrap.sh | bash
```

What this one command does, in order:

1. Downloads the official versioned release from GitHub Releases over
   HTTPS (protocol-locked; redirects cannot downgrade to HTTP).
2. **Verifies the release before executing anything**: the published
   `checksums.txt` must carry a valid Ed25519 signature from the
   official Drevanta release key, and every downloaded file must match
   its sha256. Any mismatch, missing signature, or failed verification
   aborts with **nothing changed** on your server.
3. Hands over to the tested installer, which runs preflight checks,
   installs the release under `/opt/drevanta/releases/<version>/`
   (keeping old releases for instant rollback), creates the
   unprivileged `drevanta` service user, installs the hardened systemd
   unit, starts the daemon and health-gates it.
4. On a **fresh** install it attempts public HTTPS provisioning once
   (next section) and then prints your personal one-time setup link.

> **Note — when this command becomes official.** The command above is
> real and fully tested by the installer harness, but it resolves
> against this project's GitHub Releases. It works the moment the
> repository/releases are reachable publicly; if your organization has
> not made them public yet, use the distribution mirror your operator
> gave you (`--base-url`) or the developer path in
> `docs/FOR-DEVELOPERS.md`.

Re-running the command is safe: the same version is a verified no-op, a
new version upgrades in place with automatic rollback on failed
readiness, existing claim tokens are **never** rotated by an upgrade.

### `--https auto|yes|no`

Public HTTPS is attempted **automatically on a fresh install** and never
on upgrades. To control it explicitly:

```bash
curl -fsSL https://github.com/backupamunga-ops/drevanta_server_manager/releases/latest/download/bootstrap.sh | bash -s -- --https no
```

## Step 3 — Open your setup link

### If HTTPS succeeded (the normal case)

The installer prints something like:

```text
✓ DREVANTA INSTALLED SUCCESSFULLY

  Your private server console is ready, secured with a publicly
  trusted certificate.

  OPEN DREVANTA IN YOUR BROWSER

  https://YOUR_VPS_IP/onboarding#token=drv1_...

  Your setup link expires in 30 minutes
  and can be used only once.

  Lost or expired your link?  sudo drevanta claim regenerate
```

That is the whole success screen: one action, one link. The installer
only prints it after the certificate has been issued **and** the HTTPS
route has been verified — a URL is never shown before it actually
works. No localhost URLs, SSH-tunnel commands or internal backend
addresses appear on this screen.

Open the link directly in your browser — no domain, no SSH tunnel, no
extra tools. The certificate is publicly trusted and issued for your
VPS IP address (Let's Encrypt, ~6-day lifetime, renewed automatically
by Drevanta). The `#token=...` part never leaves your browser: it is
stripped from the address bar the moment the page loads.

### If HTTPS could not be configured (private fallback)

The installer prints the reason and the complete fallback:

```text
✓ DREVANTA INSTALLED SUCCESSFULLY

  Drevanta is running securely.

  Direct HTTPS could not be configured:
  [clear reason]

  PRIVATE ACCESS

  On your computer (NOT inside this VPS shell), open a terminal and run:

  ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP

  Keep this SSH connection open. (Already connected with plain
  "ssh user@ip"? Forwarding only applies to NEW connections —
  reconnect with the command above or open a second local terminal.)

  Then open:

  http://127.0.0.1:18080/onboarding#token=drv1_...
```

Important: the `ssh -L ...` command runs **on your computer**, not on
the VPS — you cannot add port forwarding to a session that is already
open; reconnect with the command above or use a second local terminal.
This `http://127.0.0.1:...` link is safe because it only exists inside
your own SSH tunnel; it is never a public address.

To retry public HTTPS later (no reinstall needed):

```bash
sudo drevanta access enable-https
```

## Step 4 — Create your administrator account

The onboarding page shows a compact form:

- a green **✓ Token loaded · drv1_•••XXXX · Change** row when the link
  contained your token (use **Change** to paste a different token or a
  full setup link);
- **Admin username** (3–32 characters; letters, numbers, dots, dashes,
  underscores), e.g. `rootadmin`;
- **Password** (minimum 12 characters) with Generate, a visibility
  toggle and a quiet strength meter;
- **Confirm password**.

Validation is quiet: nothing shouts while you type; a short error
appears only after you leave a field (or submit), and it disappears the
moment the value becomes valid.

Click **Create administrator account**. The backend checks the token
(now, and only now — loading the page never consumes it) and creates
the account with the password stored as an argon2id hash.

## Step 5 — Set up MFA (strongly recommended)

Next, enroll an authenticator app (Aegis, Google Authenticator, 1Password,
…): scan the QR code or paste the secret, then type the current 6-digit
code to confirm.

**MFA is strongly recommended but not mandatory.** If you prefer to
continue without it, choose **Skip for now**. A warning explains the
risk (password-only sign-in), and you must tick an explicit
confirmation — the choice is recorded in the server's audit log. You
can enable MFA at any time later under **Settings → Account Security**;
administrators who enrolled keep every protection.

## Step 6 — Save your recovery codes (when MFA is enabled)

When MFA is enabled — during onboarding or later — ten one-time codes
are shown **exactly once**. Store them in a password manager or print
them. Each code replaces the 6-digit code once if you lose the
authenticator. Tick the confirmation box and continue.

## Step 7 — Choose your access address

The console is live on the server's IP address with a publicly trusted
certificate. The compact choice:

- **Continue with the server IP** (default) — ready now, no extra
  setup; you go straight to the dashboard.
- **I have a domain** — enter a hostname you own. Drevanta verifies
  its DNS actually points at this VPS, issues a publicly trusted
  certificate, verifies the full HTTPS route, and only then switches
  the recommended address to `https://your-domain/`. If anything fails
  (DNS not propagated, certificate refused), nothing is changed and the
  working server-IP address keeps serving. You can configure or change
  the domain at any time later under **Settings → Access address**.

No domain is ever required, and no account with any external service is
needed.

## Step 8 — Enter your dashboard

You land in the live server console. Next visits start at `/login`
(password + authenticator code, or a recovery code when MFA is
enabled).

---

## Managing access later

All access controls live under one roof (run on the VPS):

```bash
sudo drevanta access status          # access mode, certificate state, detected IP
sudo drevanta access enable-https    # obtain/retry the trusted certificate (no reinstall)
sudo drevanta access disable-https   # stop public HTTPS; backend and data untouched
sudo drevanta access configure       # change target IP / custom domain / staging
```

`status` shows the current mode, certificate validity and renewal state,
the detected public IP and the SSH-tunnel fallback — plus corrective
instructions whenever something is wrong.

**Custom domains are optional and never required.** The comfortable way
is the dashboard: **Settings → Access address** verifies DNS, issues the
certificate and route-verifies before switching — the server-IP
certificate keeps serving the whole time. On the CLI, `sudo drevanta
access configure --domain console.example.com` (or `--domain` with
`enable-https`) issues the certificate for a domain through the same
proxy. Either way the IP certificate is preserved and renewal is
automatic. There is no Drevanta cloud account and no central control
plane.

### Advanced access: the SSH tunnel (recovery path)

The SSH tunnel is the always-available recovery path — it works even
when public HTTPS is disabled, misconfigured, or mid-renewal. On **your
computer**:

```bash
ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP
# keep this connection open, then open:
#   http://127.0.0.1:18080/
```

The backend itself only ever listens on `127.0.0.1:8080` and is never
exposed publicly; the tunnel is private to your SSH session.

Disabling public HTTPS never stops the backend and never touches your
data — you keep the SSH-tunnel access described above.

## If the link is lost or expired

One command on the VPS prints a fresh link (and invalidates the old
token):

```bash
sudo drevanta claim regenerate
```

It prints the HTTPS link when public HTTPS is live, otherwise the SSH
tunnel link. Other claim commands: `sudo drevanta claim status`
(no secrets) and `sudo drevanta claim revoke`.

## Troubleshooting

**`port 80 is already in use by another program`** — a service other
than Drevanta (find it with `ss -tlnp | grep -E ':(80|443)\b'`) owns the
ports. Drevanta's own `drevanta-https` unit holding them is NOT an
error: upgrades recognize it and restart it to re-bind. Only a foreign
listener blocks provisioning — stop it and run
`sudo drevanta access enable-https` again.

**HTTPS not ready after install** — the proxy keeps retrying in the
background. Check `sudo drevanta access status` and
`journalctl -u drevanta-https -n 50`. The most common cause is inbound
TCP 80/443 blocked by the provider firewall/security group; opening
those ports is a provider-console change Drevanta never makes on its
own.

**The setup link expired** — `sudo drevanta claim regenerate`.

**`sudo drevanta access ...` refuses ("must run as root")** — use
`sudo`, as shown.

**Upgrades** — run the same installation command; the same version is a
no-op, a new version upgrades with automatic rollback, and the access
configuration is left untouched.

## Why the daemon only listens on loopback

The Drevanta daemon (`drevantad`) binds `127.0.0.1:8080` only. Public
HTTPS is served by a separate, minimal proxy process
(`drevanta-https`, an unprivileged systemd unit with exactly one
capability: binding low ports) that forwards to the private backend,
strips client-supplied forwarding headers, sets HSTS, and never
exposes plain HTTP. If public HTTPS is off — the fallback is and stays
the SSH tunnel over loopback.

---

## For Developers

Manual binary transfers, building from source, running the installer
directly with a staged binary, the test harness and all
development-only procedures moved to **[docs/FOR-DEVELOPERS.md](FOR-DEVELOPERS.md)**.
