# Drevanta UI design principles

These are the rules every dashboard page follows — and every new page
(Phase 2 and later) MUST follow. The reference implementation is the
Settings shell (`web/src/routes/settings/`, v0.4.19); the tokens and
shared primitives live in `web/src/app.css`.

## 1. Source of truth

- **Tokens and shared primitives:** `web/src/app.css` — never
  re-declare them inside a component's `<style>` block. Page-specific
  layout stays in the page; anything used by two pages moves to
  `app.css`.
- **Shared components:** `web/src/lib/components/` (`Sparkline`,
  `Bar`, `StatusPill`). Extract a component only on second use.
- **No CDNs, no new frameworks, no CSS/JS runtimes.** The dashboard is
  a static SvelteKit export embedded in the Go binary; system fonts
  only.

## 2. Tokens (dark-first)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0b1220` | page background |
| `--bg-card` | `#101a2d` | cards, active nav items |
| `--bg-inset` | `#0a1120` | inputs, code chips |
| `--border` | `#1e2a44` | card/input borders |
| `--text` | `#e5ecf5` | primary text |
| `--text-dim` | `#8aa0b8` | secondary text, labels |
| `--accent` | `#34d399` | primary actions, OK state |
| `--accent-warn` | `#fbbf24` | warnings |
| `--accent-crit` | `#f87171` | errors, destructive |

Never hard-code hex values in components; if a needed color is missing,
add a token first.

## 3. Layout

- Shell (`+layout.svelte`): max-width 1080, header (brand) + footer
  (access chip, version). Onboarding is the only bare, full-screen
  route.
- Page widths: auth forms 460; settings 860; dashboard full grid
  (`.grid`, auto-fit minmax(280px, 1fr)).
- Section gap 16px; card internal gap 10–12px.
- Responsive: single column below 720px. A desktop sidebar becomes a
  horizontally scrollable chip row (see `.settings-nav`).
- Every page sets `<title>` (page name — Drevanta).

## 4. Typography

- Page title `h1`: 20px / 700 (`page-head`).
- Card heading `h2`: 15px / 700 (global `.card h2`).
- Body: 13.5–14px, line-height ≥ 1.5. Secondary text `.muted`
  (13.5px, dim).
- Micro-labels: 11–12px, 700, uppercase, letter-spacing 0.06–0.1em
  (`.field label`, `.card-title`).
- Numbers in metrics: `font-variant-numeric: tabular-nums`; units via
  `.unit` (0.45em, dim). Monospace values use `.mono`.

## 5. Forms

- Structure: `.field` → `label` (micro-label) → `input` (bg-inset,
  radius 8, accent focus) → inline message.
- Inline validation and API errors: `.field-error` (crit text, NO box)
  with `role="alert"` and `aria-invalid` on the input.
- Page-level fetch failures: global `.error` box.
- Multi-field groups stack via `.stack`; button rows via `.row`
  (wraps on small screens).
- Confirmations that change state say what will happen ("Changing the
  password signs out every other device.") in `.hint`.
- Passwords: `autocomplete="current-password"` / `new-password`,
  Show/Hide toggle, 128 max; codes are `.mono` with `inputmode="numeric"`
  and `autocomplete="one-time-code"`.

## 6. Buttons

- `.btn-primary` — accent background, dark text; the single main
  action per form. Disabled while busy or invalid; busy state shows an
  ellipsis label ("Saving…").
- `.btn-ghost` — transparent + border; secondary and destructive-lite
  actions (Cancel, Copy, Sign out everywhere, Remove domain).
- `.btn-danger` — crit outline; reserved for genuinely destructive
  operations (Phase 2: firewall/SSH apply, deletes).
- One primary button per view section; never a ghost for the primary
  path.

## 7. Status and honesty

- Status pills: `StatusPill` on the dashboard; `.pill` + `.pill-ok` /
  `.pill-warn` / `.pill-crit` inline. A pill NEVER carries information
  alone — the surrounding text states the fact ("MFA off — your
  account is protected by password only").
- Success feedback: `.ok-line`; warnings: `.warn-line`; hints:
  `.hint`.
- Loading states are explicit ("loading…"), never skeletons that look
  like content; a failed fetch shows an error, not a blank card.
- **No fake buttons.** A feature that does not exist yet gets no
  button (Phase 2 PRD principle 7 and UX-010: an empty state explains
  whether a feature is unavailable, unauthorized, or merely empty).
- Destructive or access-affecting operations require an explicit
  confirmation step (Phase 2 preflight screens build on this).

## 8. Accessibility minimums

- Keyboard operable: nav items are real `<button>`s; active section
  gets `aria-current="page"`; focus-visible uses the accent border.
- Every input has a bound `<label for>`; every region has an
  `aria-labelledby` heading; live errors have `role="alert"`.
- Severity is never color-only (pill text + wording carry it).
- Charts get text alternatives (sparkline + numeric value in the card).

## 9. Documentation screenshots

Desktop 1440×900, mobile 390×844; one pair per step, desktop left /
mobile right, one shared caption (see
`docs/AUTHENTICATION_AND_ONBOARDING.md`). Re-capture whenever a page's
markup changes.
