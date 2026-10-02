# VaanarSena

Open source, self-hosted MDM for iOS, iPadOS, macOS, Windows, Android, ChromeOS
and Linux, company-owned and personal (BYOD). One Go binary serves the device
protocols, the REST API and the web console; PostgreSQL holds the state.
Apache 2.0. User docs: https://dmdhrumilmistry.github.io/VaanarSena/ (source in
`docs/`).

## Layout

| Path | What |
|---|---|
| `cmd/vaanarsena` | server (`serve`) |
| `cmd/vaanarsena-agent` | Linux agent: enroll, mTLS check-in, inventory, commands, local policy |
| `cmd/vsctl` | CLI: apply, diff and export manifests |
| `internal/api` | REST API and role checks (the `auditor`, `operator` and `admin` route wrappers in `api.go`) |
| `internal/store` | PostgreSQL access; `migrations/NNNN_name.sql` are embedded and applied on start |
| `internal/command` | command catalogue and the BYOD guard (`Authorize`, `AuthorizeInternal`) |
| `internal/mdm` | platform-neutral service: enrollment, effective policy, queueing |
| `internal/apple`, `windows`, `android`, `chromeos`, `agent` | per-platform drivers |
| `internal/policy` | neutral policy document and per-platform translators |
| `internal/groups`, `blueprint`, `manifest` | smart groups, blueprints, `vaanarsena.io/v1` manifests |
| `internal/platforms` | Apple, Google, Android and ChromeOS credentials set from the console |
| `internal/pki` | device CA, client certificate parsing (nginx, Caddy, Traefik headers) |
| `internal/web/static` | console: vanilla ES modules, no build step, embedded with `go:embed` |
| `internal/e2e` | end-to-end suite against a real PostgreSQL |
| `test/` | dev server, seed data, browser tests, Docker agent test (see Testing) |
| `deploy/` | Docker Compose (plain, and Caddy for HTTPS); the Helm chart lives in github.com/dmdhrumilmistry/helm-charts |
| `docs/`, `mkdocs.yml` | MkDocs Material site, published to GitHub Pages by `.github/workflows/docs.yml` |

## Rules

- **ASCII only in everything you write**: code, comments, SQL, UI copy, docs,
  commit messages. Never use em or en dashes; use a hyphen, comma, colon or
  parentheses. Plain quotes, `...` not the ellipsis character.
- **Original code only.** Implement from the public specs (Apple Device
  Management, MS-MDE2/MS-MDM and the CSP docs, Android Management API, Admin
  SDK). Do not copy from other MDMs (Fleet, MicroMDM, NanoMDM...), even if
  permissively licensed. If a protocol detail cannot be verified, say so in a
  comment and do not guess.
- **BYOD is enforced on the server**, never only in the UI. Personal devices
  get fewer commands (`command.Authorize`), no hardware identifiers, and limited
  software inventory (Linux and Windows: none; Apple: managed apps; Android:
  work profile). Any change here needs an e2e test that fails when the rule is
  removed (check it by removing the rule once).
- **The API is a contract**: add fields and endpoints, do not rename or remove.
  `vsctl`, manifests and users' automation depend on it. Document new endpoints
  in `docs/api.md`.
- **Migrations are append-only**: add `internal/store/migrations/NNNN_*.sql`,
  never edit a shipped one.
- **Never commit secrets** (real passwords, keys, tokens, `.env` files). Dev
  defaults in `test/dev` are local-only throwaways.
- **Docker hygiene**: never `docker system prune`, `container prune` or similar
  on a developer machine. Remove only containers you created, by name.

## Console (internal/web/static)

- No framework, no bundler, no CDN. CSP is `script-src 'self'; style-src 'self'`:
  no inline `<style>`, no `style=""` in markup (setting `el.style` from JS is fine).
- Build DOM with `h()` from `js/core.js`; untrusted text only via textContent.
  Never `innerHTML` with data.
- Reuse the components in `core.js` and `forms.js` (`page`, `panel`, `table`,
  `tabBar`, `drawer`, `menu`, `kpi`, `skeleton`, `chip`, `badge`, `emptyState`,
  `field`, `toggle`, `segmented`, `downloadCsv`...) and the color tokens in
  `css/tokens.css` (light, dark, and system via `prefers-color-scheme`).
- CSS: shared rules in `components.css`, `base.css`, `shell.css`; view rules in
  `fleet.css`, `configure.css` or `admin.css`. Give new view classes a
  view-specific prefix (`sw-`, `mf-`, `bp-`...): two views both defining
  `.editor-main` once broke the policy editor's sticky layout.
- Copy is sentence case, active voice, buttons say what they do ("Save
  changes"), errors say what happened and how to fix it. Check contrast (WCAG
  AA) in both themes and layout at 390 px.
- Editors with unsaved changes set `state.leaveGuard` (see `saveBar` in
  `policy-form.js`); `go()` and back/forward honor it.

## Testing

Run what matches the change; run all of it before a release.

### Go: unit and end-to-end

```bash
make lint                     # gofmt
go vet ./...
go test -race ./...           # unit tests, no dependencies
make e2e                      # starts PostgreSQL container vs-e2e-pg on :55432, runs internal/e2e, removes it
```

`internal/e2e` **skips silently** when `VS_TEST_DATABASE_URL` is unset, so a
plain `go test ./...` passing proves nothing about it. Against an existing
database (it is wiped):

```bash
VS_TEST_DATABASE_URL='postgres://vs:vs@localhost:55432/vs_test?sslmode=disable' \
  go test -count=1 -v ./internal/e2e/     # look for --- PASS, not --- SKIP
```

Add e2e coverage for anything touching auth, roles, device identity, the BYOD
rules, manifests or inventory. Platform protocol parsing (plist, SyncML, AMAPI
JSON) gets unit tests with realistic sample payloads next to the driver.

### Console: dev server and browser tests

```bash
test/dev/start.sh                       # https://localhost:18443, admin@example.com / correct-horse-battery
bash test/dev/seed.sh                   # once per fresh database: six Linux devices (one personal)
python test/dev/seed-inventory.py       # their apps and services
cd test/ui && npm install
node tour.js out light 1440             # screenshot every page, report JS errors
node tour.js out dark 1440
node tour.js out light 390
node flows.js                           # drive real flows, verify through the API
node guard.js                           # unsaved-changes guard on browser back
```

- `start.sh` runs PostgreSQL in container `vs-dev-pg` (port 55431, kept
  between runs; `VS_DEV_PG_PORT` overrides) and serves the console from disk
  (`VS_WEB_DIR`), so UI edits need only a reload. Go changes need a restart. To
  start over, stop the server and `docker rm -f vs-dev-pg`.
- Browser tests use an installed browser through `playwright-core` (Edge on
  Windows, Chrome elsewhere; `VS_UI_CHANNEL` overrides). `VS_UI_BASE`,
  `VS_UI_EMAIL` and `VS_UI_PASSWORD` point them at another server.
- `tour.js` and `flows.js` must report zero JS errors and zero FAIL lines.
  **Look at the screenshots** (light, dark, 390 px); errors-free is not the same
  as looking right. When markup changes, update selectors in these scripts,
  never weaken a check. New pages go in `tour.js` `PAGES`; new behavior gets a
  check in `flows.js`.
- `node --check` every `internal/web/static/js/**/*.js` (CI does).

### Docker image with real agents

```bash
bash test/docker/agents.sh          # KEEP=1 to leave it running
```

Builds the image from the checkout, runs `deploy/docker-compose.yml` as project
`vs-dockertest` with TLS on https://localhost:18843, enrolls the real agent in
Ubuntu, Arch, AlmaLinux (systemd as PID 1) and a personal Ubuntu container, and
checks stored inventory against each distro's own package tools. Cleans up only
its own containers and volume. Run it for changes to the agent, the agent
protocol, inventory, the Dockerfile or the compose file.

### Docs

```bash
pip install mkdocs-material==9.* && mkdocs build --strict -d site
cd test/ui && node docshots.js ../../docs/assets/screens   # refresh screenshots (needs the seeded dev server)
```

Update `docs/` in the same change as the behavior (console guide, API table,
platform notes, security for anything BYOD).

## Windows and Git Bash notes

- `MSYS_NO_PATHCONV=1` stops Git Bash rewriting arguments like `/CN=localhost`,
  but then native tools (openssl, python, go, docker) get POSIX paths they
  cannot open. Pass file paths through `cygpath -m` (see `hostpath()` in the
  `test/` scripts).
- Hyper-V reserves port ranges (check
  `netsh interface ipv4 show excludedportrange protocol=tcp`); pick ports
  outside them.
- Windows curl cannot use PEM client certificates; `test/dev/checkin.py` does
  mTLS check-ins with Python's ssl instead.

## Release

- Push to `main` publishes `ghcr.io/dmdhrumilmistry/vaanarsena:main`; a `vX.Y.Z`
  tag publishes the versioned image, `:latest` and the agent binaries.
- Then bump `version` and `appVersion` in the chart in
  github.com/dmdhrumilmistry/helm-charts and update its README if values changed.
- CI (`ci.yml`) runs gofmt, vet, race tests, builds, `node --check` and the e2e
  suite; wait for it to pass (`gh run watch`).

## Before you commit

1. gofmt, vet, unit tests, and e2e with `VS_TEST_DATABASE_URL` set (PASS, not SKIP).
2. For UI changes: tour (light, dark, 390) and flows clean, screenshots reviewed.
3. For agent, protocol or image changes: `test/docker/agents.sh`.
4. No em or en dashes in the diff:
   `git diff --cached | grep -nP '[\x{2013}\x{2014}]'` prints nothing.
5. Docs updated. Commit subject imperative and under 72 characters, body says why.
