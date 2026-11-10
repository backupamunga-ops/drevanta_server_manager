# Installing Drevanta — the complete first-time guide

This guide is written for someone who has just rented a VPS (a small
Linux server in the cloud) and wants Drevanta running on it. You do
**not** need to be a professional system administrator. Every command
you need is written out in full. By the end you will have:

1. installed Drevanta with **one command**,
2. received a **one-time setup link** in your terminal,
3. created your administrator account in the browser,
4. configured two-factor authentication (MFA), and
5. reached your dashboard.

> **Good to know before you start:** Drevanta's dashboard is deliberately
> *not* on the public internet. It listens only on the server itself
> (`127.0.0.1:8080`), and you reach it through an encrypted SSH tunnel —
> the same channel you are already using to administer the server.
> Section 13 explains why this is a feature, not a limitation.

---

## 1. VPS prerequisites and supported operating systems

| Requirement    | Minimum                                | Notes                                                |
| -------------- | -------------------------------------- | ---------------------------------------------------- |
| Operating system | Ubuntu 20.04+ or Debian 11+ (64-bit) | Other systemd distributions work but are untested    |
| Architecture   | x86_64 (amd64)                         | The release binary is built for linux/amd64          |
| Memory         | 256 MiB RAM (512 MiB comfortable)      | Drevanta is capped at 256 MiB by its systemd unit    |
| Disk           | 256 MiB free under `/opt` and `/var/lib` | Releases + SQLite database + audit log             |
| Init system    | systemd (running)                      | `drevanta` runs as a hardened systemd service        |
| Access         | SSH into the server as root or a sudo-capable user | You need this for installation only       |
| Tools on the server | `curl`, `systemd` (always present on Ubuntu/Debian) | The installer checks everything for you  |

You also need the release artifact on the server: a single binary file
named `drevantad-linux-amd64`, plus the `deploy/install.sh` script. If
someone built Drevanta for you, they will have given you both. If you
build it yourself from the repository: `make cross-linux-amd64`.

**You do NOT need:** Docker, a web server, a domain name, TLS
certificates, or any open port besides SSH (22). Drevanta refuses that
extra machinery on purpose.

## 2. One-command installation

Copy the binary and the installer onto the server (from **your
computer**, replacing the IP):

```sh
scp drevantad-linux-amd64 install.sh root@YOUR_VPS_IP:/root/
```

Then log in and run the installer:

```sh
ssh root@YOUR_VPS_IP
sudo install.sh install --binary /root/drevantad-linux-amd64
```

That single command performs, in order: preflight checks (nothing is
changed if any check fails), service-user creation, release install,
hardened systemd unit, start, a health verification, and — for a fresh
installation — the issuance of your **one-time claim token**.

Two optional variations:

```sh
sudo install.sh preflight --binary /root/drevantad-linux-amd64  # checks only, changes nothing
sudo install.sh install --binary /root/drevantad-linux-amd64 --skip-claim  # install now, token later
```

## 3. The installer success screen (example)

On a successful **interactive** installation (you ran it in your own
terminal) you will see a screen like this — the token shown here is
**redacted for the example**; yours will be a full 48-character code:

```
✓ DREVANTA INSTALLED SUCCESSFULLY

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Welcome to Drevanta!

  Your server is ready for first-time setup.

  STEP 1 - On your computer, run:

  ssh -N -L 18080:127.0.0.1:8080 root@203.0.113.10

  STEP 2 - Open this link:

  http://127.0.0.1:18080/onboarding#token=drv1_Xj2kQ9mFpLw8vRtY3bN7cH5sD6gJ4aKeU2zPqM8xCvBn

  Token expires in 30 minutes.
  This link can be used only once.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Raw token (fallback): drv1_Xj2kQ9mFpLw8vRtY3bN7cH5sD6gJ4aKeU2zPqM8xCvBn

  Lost this link or it expired? Run on the VPS:
  sudo drevanta claim regenerate

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

Everything you need is on this screen: the SSH tunnel command (STEP 1),
the ready-to-open setup link (STEP 2), how long the link stays valid,
and a reminder that it works exactly once. **Leave this terminal window
open until you have opened the link** — the token is shown only this
once. If you close it anyway, Section 5 shows the one command that gets
you a fresh link.

If the installer ran without a terminal attached (automation, CI), it
never prints the token. Instead it writes it to a root-only file
(`/root/.drevanta-claim-token`, mode 0600) and tells you the path; read
it with `sudo cat /root/.drevanta-claim-token` and delete the file once
onboarding is done.

## 4. What the claim token is, and how it is delivered

The claim token is a **single-use, expiring key** that proves "I am the
rightful owner of this fresh server" when you create the first
administrator account. How it works:

- It is generated with a cryptographically secure random generator at
  the end of installation and printed **once**, directly to your
  terminal.
- It expires after **30 minutes** by default (`--ttl` can change this
  at install time; `sudo drevanta claim regenerate` re-issues at the
  default window).
- The server stores only a cryptographic **hash** of the token — anyone
  who steals the database cannot use it.
- Opening the setup link does **not** consume the token. Only
  successfully creating the first administrator account consumes it.
- After the first administrator exists, the token system is retired for
  your instance: regeneration is **permanently refused**, and no new
  claim token can ever be minted. You then sign in with your password
  and MFA like everyone else.

If an active token already exists, a re-run of the installer will **not**
rotate it (rotating would silently invalidate the link you may still be
holding). Rotation only happens when you explicitly ask for it — see the
next section.

## 5. Lost or expired the link? One command fixes it

If the 30 minutes passed, the terminal was closed, or the link was lost,
run this **on the VPS**:

```sh
sudo drevanta claim regenerate
```

That prints a fresh screen — new token, new expiry, the same tunnel
command and a new ready-to-open link — and immediately invalidates the
previous token, so only the newest link can ever be used. You can do
this as many times as you like **until the first administrator account
is created**; after that, the command permanently refuses:

```
instance is already claimed; no claim token is issued
```

(That refusal is the security working as intended: once an owner
exists, a leaked link cannot hand your server to someone else.)

Technical note: `sudo drevanta` is a friendly front-end installed by the
installer. The exact same result, without the front-end, is:

```sh
sudo -u drevanta /opt/drevanta/current/drevantad claim init --data-dir /var/lib/drevanta
```

You never need the long form; it is listed here only so you know what
the short command maps to.

## 6. Opening the tunnel from Windows (PowerShell)

Windows 10 (1809+) and Windows 11 ship with a built-in SSH client —
no installation needed. On **your Windows computer**:

1. Click **Start**, type `powershell`, press **Enter**.
2. In the blue PowerShell window, paste (replace the user and IP with
   the values from the STEP 1 line of your success screen):

   ```powershell
   ssh -N -L 18080:127.0.0.1:8080 root@YOUR_VPS_IP
   ```

3. The first time, it asks to confirm the server's fingerprint: type
   `yes` and press **Enter**. Then enter the server password when
   prompted (or nothing, if you use a key).
4. **The window will look like it is hanging — that is correct.** An
   SSH tunnel with `-N` opens no remote shell; it just forwards. Keep
   this window open the whole time you are using the dashboard.
   Closing the window closes the tunnel.

If PowerShell says `ssh` is not recognized, enable the optional
feature: *Settings → Apps → Optional features → Add feature → OpenSSH
Client*.

## 7. Opening the tunnel from Linux or macOS

On **your computer**, in any terminal:

```sh
ssh -N -L 18080:127.0.0.1:8080 root@YOUR_VPS_IP
```

Same notes as Windows: confirm the fingerprint once, enter your
password or let your key do the work, and **leave the terminal open**.
The command produces no output while it runs — silence means the tunnel
is up.

To run it in the background instead (macOS/Linux), add `-f`:

```sh
ssh -fN -L 18080:127.0.0.1:8080 root@YOUR_VPS_IP
# later, to close it:
pkill -f "ssh -fN -L 18080"
```

## 8. Opening the token-prefilled onboarding URL

With the tunnel running, open **STEP 2's link** from the success screen
in your browser:

```
http://127.0.0.1:18080/onboarding#token=drv1_…
```

What happens:

- The `#token=…` part (the *fragment*) never travels to any network —
  your browser keeps fragments local. Drevanta additionally scrubs the
  token out of the address bar and browser history immediately after
  prefilling the field, so it does not linger in autofill or session
  restore.
- The onboarding page shows the token field **already filled in**. If
  you opened plain `http://127.0.0.1:18080/onboarding` instead (no
  fragment), simply paste the raw token from your terminal into the
  field.
- Nothing is sent anywhere except to Drevanta itself, through the
  tunnel, on your own machine.

> The page will only load while the tunnel from Sections 6/7 is running.
> If the browser cannot connect, see Section 12.

## 9. Creating the first administrator account

On the onboarding page:

1. Check the pre-filled token (or paste the raw one) and continue.
2. Choose your **administrator username** (3–32 characters: letters,
   digits, dots, underscores, hyphens).
3. Choose a **password of at least 12 characters** — a passphrase of
   several unrelated words is both stronger and easier to remember.
   Drevanta stores only an Argon2id hash of it; nobody (including you
   via support) can read it back.
4. Submit. This single submission consumes the claim token and binds
   the account as the **first owner** — from this moment the claim
   token is dead forever, and no new one can be created.

## 10. Configuring MFA and saving your recovery codes

The wizard then asks you to set up two-factor authentication:

1. Scan the displayed QR code with any TOTP authenticator app — Google
   Authenticator, Aegis, 1Password, Raivo, KeePassXC all work. (Or type
   the shown secret manually into the app.)
2. Enter the **current 6-digit code** from the app to confirm the setup.
3. The wizard then shows **10 recovery codes, exactly once**. Save them
   properly: download/copy them into a password manager, or print them.
   Each code works a single time as a replacement for the 6-digit code
   at login — this is what gets you in if you lose your phone.
4. If you genuinely cannot use an authenticator app right now, the
   wizard offers an explicit, audited **MFA skip** — you can enable MFA
   later. Skipping is recorded in the audit log; it is never silent.

**Do not skip saving the recovery codes.** Without the authenticator
and without recovery codes, regaining access means wiping and
re-onboarding the instance.

## 11. Signing in and using the dashboard

After onboarding you land on the dashboard. Every later visit:

1. Start the tunnel (Sections 6/7) — it is only needed while the
   dashboard is open in a tab.
2. Open `http://127.0.0.1:18080/login`.
3. Sign in with username + password + the current 6-digit code from
   your authenticator app. (Lost phone? Use a recovery code from
   Section 10 in place of the 6-digit code.)
4. Sessions expire automatically after a sliding window (and a hard
   cap); you simply sign in again when that happens.

The dashboard shows live CPU, memory, disk, load and uptime. Metrics
are read on demand — Drevanta never runs background samplers.

## 12. Troubleshooting

**The browser says "connection refused" / "can't connect"**
- Is the tunnel still running? The `ssh -N -L …` window must be open
  (or `-fN` process alive: `pgrep -af "ssh.*18080"`).
- Did you open `http://127.0.0.1:18080` (port **18080**, the local end
  of the tunnel)? `http://127.0.0.1:8080` and
  `http://YOUR_VPS_IP:8080` will *not* work — 8080 exists only on the
  server's loopback, and 18080 only exists on your computer while the
  tunnel is up.
- On the VPS, confirm the daemon is healthy:
  `systemctl status drevanta` and
  `curl -fsS http://127.0.0.1:8080/readyz` (should say `"status":"ready"`).

**"This site can't be reached" only from a different device**
That is by design — the dashboard exists on the machine that holds the
SSH connection. Run the tunnel on the device that needs access, or use
SSH agent forwarding from a jump host. Do not open port 8080 in the
firewall.

**Token rejected: "expired" / "invalid" / "already used"**
- Expired (30 minutes passed): run `sudo drevanta claim regenerate` on
  the VPS and use the fresh link.
- Invalid: the link was copied incompletely — compare with the *Raw
  token* line; it starts with `drv1_` and is 48 characters total.
- Already used: the administrator account was created with this link —
  go to `/login` instead of `/onboarding`.
- Token was invalidated by a regeneration: only the **newest** printed
  link works; older ones are dead by design.

**I closed the terminal before copying the link**
Nothing is lost. On the VPS: `sudo drevanta claim regenerate`. That is
exactly what the command is for. (The installer also never stores the
token in logs — you cannot "scroll back" via journalctl, and neither
can anyone else.)

**The tunnel drops when my laptop sleeps**
Just re-run the same `ssh -N -L …` command. The URL does not change and
an unused, unexpired token stays valid until its expiry.

**Wrong port / address in the tunnel command**
The success screen derives `user@ip` from your actual SSH session. If
it printed placeholders (`<ssh-user>@<your-vps-ip>`), replace them with
the address you used to log in to this very server. `18080` is the
conventional local side; any free port works if you also change the
number in the URL.

**"instance is already claimed; no claim token is issued"**
The first administrator already exists. There is no claim token to get
— sign in at `http://127.0.0.1:18080/login`. If you are locked out
entirely, the documented recovery is a full reset (see DEPLOY.md,
`--uninstall --purge`), which erases accounts, MFA and the audit log —
it is deliberately loud.

**Onboarding page loads but looks broken / login loops**
Force-refresh (Ctrl+Shift+R). The dashboard is embedded in the binary;
a stale cached page from an old release is the usual culprit.

## 13. Why Drevanta only listens on loopback (the default, by design)

Most server panels punch a hole in the firewall and add a login page on
the public internet. That page then has to defend itself against every
bot on the planet: credential stuffing, zero-day scanners, TLS
terminator bugs, brute force. On a small VPS, that is a lot of attack
surface for little benefit.

Drevanta inverts the model:

- The dashboard binds to `127.0.0.1:8080` — a loopback address that is
  unreachable from any network. Port scans see nothing.
- The only way in is an SSH tunnel: the exact credentials and
  encryption you already trust for server administration guard the
  management UI as well. No second login surface exists to attack.
- Onboarding is equally private: the one-time setup link works through
  the tunnel only. The claim token itself is single-use, expiring,
  hash-stored, and shown once — even a leaked database row is useless.
- The installer never touches your firewall, SSH configuration, or any
  unrelated service.

You already had a secure management channel — SSH. Drevanta simply uses
it instead of inventing a new, weaker one.

## 14. Future option: public HTTPS access

Loopback-only is the default and will remain it. If you later want the
dashboard reachable from a browser without a tunnel — for a team, or
from a device where you cannot open tunnels — the planned, optional
model is:

1. A **domain name** pointed at the VPS (recommended), or a public IP
   with a correctly issued certificate.
2. A separate, independently-sandboxed TLS reverse proxy (for example
   Caddy or nginx with Let's Encrypt) that terminates HTTPS and proxies
   **only** to `127.0.0.1:8080`.
3. Explicit operator action to enable it; nothing in Drevanta will open
   ports, install proxies, or change TLS configuration by itself.

Until that feature exists and you opt in, do not expose port 8080. The
SSH tunnel gives you the same dashboard today with no extra risk.

---

*Command reference — everything in this guide at a glance:*

| Where          | Command / URL                                            | Purpose                          |
| -------------- | -------------------------------------------------------- | -------------------------------- |
| VPS            | `sudo drevanta claim regenerate`                         | Fresh one-time setup link        |
| VPS            | `sudo drevanta claim status`                             | Onboarding state (no secrets)    |
| VPS            | `sudo drevanta claim revoke`                             | Invalidate the current token     |
| VPS            | `systemctl status drevanta`                              | Daemon health                    |
| Your computer  | `ssh -N -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP`        | Open the access tunnel           |
| Your browser   | `http://127.0.0.1:18080/onboarding#token=…`              | First-time setup (once)          |
| Your browser   | `http://127.0.0.1:18080/login`                           | Everyday sign-in                 |
