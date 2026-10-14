# Local NPM test harness

A throwaway stack with a **real Nginx Proxy Manager** and two `traefik/whoami` apps, used to test backsoon end to end. It never touches production: its own Docker project, networks and volumes, loopback-only ports and test-only credentials.

## Quick path

```sh
sh test/harness.sh up        # pulls images, starts, bootstraps, verifies (~10 s once images are cached)
curl -H 'Host: app1.test' http://127.0.0.1:8080/    # -> whoami output, "Name: app1"
sh test/harness.sh down      # removes ONLY this project's containers, networks, volumes
```

Prerequisites: Docker with Compose v2, `curl`, `python3` (stdlib only). Nothing is installed globally.

If `127.0.0.1:8080` or `:8181` is taken, `up` stops before creating anything. Pick other ports:

```sh
BACKSOON_TEST_HTTP_PORT=18080 BACKSOON_TEST_ADMIN_PORT=18181 sh test/harness.sh up
```

## Interface contract

| Item | Value |
|------|-------|
| Compose project | `backsoon-e2e` (override: `BACKSOON_TEST_PROJECT`, must start with `backsoon-`) |
| Published ports | `127.0.0.1:${BACKSOON_TEST_HTTP_PORT:-8080}→npm:80`, `127.0.0.1:${BACKSOON_TEST_ADMIN_PORT:-8181}→npm:81`; nothing else |
| Services | `npm`, `app1`, `app2` (always); production-built `backsoon` (profile `backsoon`, start with `BACKSOON_TEST_WITH_BACKSOON=1`); `fixtures` and private `app-errors` (profile `tools`); `pages-init` and the production-built panel `backsoon-admin` (profile `admin`, start with `BACKSOON_TEST_WITH_ADMIN=1`) |
| Proxy hosts | `app1.test → app1:80`, `app2.test → app2:80`, `advanced_config` empty |
| NPM user | `admin@backsoon.test` / built-in password, **test-only** (`BACKSOON_TEST_NPM_EMAIL`, `BACKSOON_TEST_NPM_PASSWORD`) |
| Volumes | `npm-data`, `npm-letsencrypt`, `npm-custom` (→ `/data/nginx/custom`, writable), `pages`, `meta` (panel only) |
| Labels | every container, network and volume has `com.backsoon.test=true` |
| Commands | `up`, `down`, `status`, `check`, `bootstrap`, `fixtures`, `compose …`, `help` |

For `test/e2e.sh`: run compose through `sh test/harness.sh compose <args>` (right file, project and profiles). Examples: `compose stop app1`, `compose cp npm/server_proxy.conf npm:/data/nginx/custom/`, `compose exec npm nginx -s reload`.

### Pages and the custom nginx directory

- `pages` is written only by `sh test/harness.sh fixtures` (clears the volume, copies `test/fixtures/pages/`). `backsoon` mounts it read-only at `/pages`.
- `npm-custom` is a separate volume, so `server_proxy.conf` can be installed or reset without editing project sources.
- `backsoon` builds `nginx/Dockerfile`, so its worker limit, entrypoint validation and envsubst filter match production. The image tag is `<project>-service:local`; it rebuilds on startup. The template bind remains overridable with `BACKSOON_TEST_TEMPLATE`. A missing template fails the start; it does not create a directory.
- Panel mode (`BACKSOON_TEST_WITH_ADMIN=1`): `up` also starts `backsoon` and `backsoon-admin`, adds the proxy host `admin.test → backsoon-admin:8080` and does **not** install the root-owned page fixtures. `pages-init` seeds the `pages` volume and hands it to uid 65532 with exactly the command of `backsoon-init` in `docker-compose.yml` (`test/lint.sh` compares the two). The panel is built from `admin/Dockerfile`, tagged `<project>-admin:local`, uses test-only credentials (`admin` / `backsoon-test-only-panel-pw`, bcrypt hash in `compose.test.yml`) and is reachable only through NPM (`Host: admin.test`).
- `app-errors` serves deterministic 200/502/503/504 from `test/fixtures/error-backend.conf` on the private network only. It is started by the e2e runner, not ordinary `up`.

## Pinned images

| Image | Tag |
|-------|-----|
| `jc21/nginx-proxy-manager` | `2.16.0` (override: `BACKSOON_TEST_NPM_IMAGE`, e.g. `jc21/nginx-proxy-manager:2.15.1`) |
| `traefik/whoami` | `v1.12.0` |
| `nginx` (base for built `backsoon`, private `app-errors`) | `1.30.5-alpine-slim`, digest `32463212…` (`nginx/Dockerfile` and test compose) |

## How `bootstrap` talks to NPM 2.16

Verified against the official image source (`routes/main.js`, `routes/users.js`, `routes/tokens.js`). The legacy `admin@example.com` / `changeme` login does not exist.

| Step | Request | Notes |
|------|---------|-------|
| 1 | `GET /api/` | Ready when `status` is `OK`; `setup` says whether a user exists |
| 2 | `POST /api/users` | Only while `setup` is `false`, unauthenticated; the first user becomes admin. Body: `name`, `nickname`, `email`, `auth {type: password, secret}` |
| 3 | `POST /api/tokens` | `{identity, secret}` → `token`, kept in memory only |
| 4 | `GET/POST/PUT /api/nginx/proxy-hosts` | Creates missing hosts, corrects drifted ones (`updated: …`), leaves the rest `unchanged` |

Safe to re-run. Secrets and tokens are never printed; the password travels through the environment, not argv. The script refuses any non-loopback URL.

## Design notes

- **Two networks.** Docker cannot publish ports from an `internal` network, so only `npm` joins a small `edge` bridge. `npm`, the apps and `backsoon` share the `internal` `testnet`; the apps have no route outside.
- **No app healthcheck.** `whoami` is a `scratch` image (no shell). The harness probes apps from inside `npm` (`direct_ok`) and through the proxy (`proxied_ok`).
- **No TLS.** It does not affect `error_page`.

## Disposable integration tests

```sh
BACKSOON_TEST_HTTP_PORT=18080 BACKSOON_TEST_ADMIN_PORT=18181 make e2e
```

`make e2e` requires a fresh project (no existing containers or volumes), installs the global snippet only into that project's NPM volume, checks stopped/upstream-error/recovery scenarios and removes only the resources it created. Do not run it against a manually running harness: choose another `BACKSOON_TEST_PROJECT` and free ports, or explicitly stop the throwaway harness first. Failure still runs cleanup; test artifacts (latency records, compiled NPM config, missing-backend headers/body/timing/exit status, worker-warming logs) remain under gitignored `.odd-checks/e2e.*`.

`make e2e` runs `test/e2e.sh` and then `test/admin-e2e.sh`, one after the other, each in its own fresh project (`make e2e-npm` / `make e2e-admin` run one). Both need the same loopback ports; never run them in parallel.

Coverage: original healthy whoami behavior (including a live application's own `/healthz` before and after the global file, and `/healthz` and `/__backsoon` on a stopped application receiving the domain maintenance page with no redirect, since the internal fallback request uses the fixed URI `/` while the client URL is unchanged), host→target→default and `$server:$port`, GET/POST/HEAD/JSON, unique headers even when an upstream sends its own, 502/503/504, NPM validation/reload/restart without backsoon, automatic recovery, disable/restore and the legitimate-503 variant.

### Panel suite (`test/admin-e2e.sh`)

Runs the panel through real NPM (`Host: admin.test`, so `X-Real-IP` and the proxy path are exercised) next to a real `backsoon`, in the default project `backsoon-e2e-admin`; it removes its own containers, volumes, networks and images on exit and keeps artifacts under `.odd-checks/admin-e2e.*` (volume ownership, image size, memory, panel log). The assertions rest on contracts only: routes, statuses, headers (CSP, `Location`, `Retry-After`, `Set-Cookie` flags), form field names, the page names and destinations it submits, and what `backsoon` and NPM publish; never headings, button text or CSS classes. Coverage: CA-17 (no session: 303/401, `POST /pages` creates nothing, the panel refuses to start without user, password or secret), login through the real form and its token cookie, CA-24 (the sixth failed login answers 429 with `Retry-After`, even with the right password), CA-18/19/20 (listing, uploads, 413/422, template escaping, normalization, 409 move confirmation), CA-14 (external-resource confirmation), CA-21 (exact sandbox CSP, 403 on missing or wrong CSRF), CA-6 (create, replace and delete are visible on the next request through NPM with no restart, plus `service:port` pages), RNF-4(a) (panel stopped, `backsoon` keeps serving), RNF-3/RNF-2 (healthy, image < 30 MB, memory < 50 MiB) and RNF-9 (panel log). It also proves the volume hand-over: `/pages`, `/pages/host`, `/pages/target` and `default.html` are owned by 65532, published files are `0644`, and the nginx worker serves every file the panel created.

NPM caches the address of a stopped container per worker (see below), so the suite reloads NPM after stopping `app1` or the panel; otherwise the first request would wait for NPM's 90 s connect timeout.

### Reload barrier

NPM reloads asynchronously, so `nginx -t`, `nginx -T` or one successful response is not proof that every worker uses the new file. After each reload (install, variant, rollback, absent backend) the runner snapshots the NPM worker PIDs from `/proc` beforehand and waits until all of them have exited. It never polls for a desired HTTP response.

### Missing-backend check

NPM 2.16 sets `proxy_connect_timeout 90s` and `resolver 127.0.0.11 valid=10s`, and each worker keeps its own resolver cache. A stopped `backsoon` with a still-cached address therefore used to hang the fallback. The runner reproduces that precondition faithfully without querying the stopped `app1`: it creates a disposable proxy host `warm-backsoon.test` (empty advanced config) pointing at the live `app-errors` fixture on closed port 1, so each warm-up request gets an immediate connection refusal and falls back to `backsoon`. NPM's resolver cache is shared by the proxy hosts of a worker, so this primes the `backsoon` address. It first reloads with the old-generation barrier, so workers are fresh and the `app1` cache is cold, then sends 150 fixed requests. Each must have curl exit 0, HTTP 503, `X-Backsoon: 1` and the maintenance page; every current worker PID must appear in NPM's own error log with `connect() failed (111` for that host and URI. It then stops `backsoon` and requires that the window from the first warm-up request stays within 8 s. If any condition fails, curl's exit status/stderr and NPM's error/access logs are saved under `.odd-checks/e2e.*` and the run stops before the probe. The single probe then uses the original 10 s client limit and must receive NPM's native generic `502` (no `X-Backsoon`, `Retry-After` or `no-store`).

### Upstream trace

For the controlled HTTP `502`/maintenance case NPM logs `upstream_status` as `502 : 503` with client status `503`; the test asserts that exact observed case. A DNS-resolution failure may log `-, 503`, so it is not asserted as the same field.

### RNF-5 latency method

One predeclared method, run once per `make e2e` and only after every functional check passed (any functional failure skips it and fails the run): eight alternating blocks A/B/A/B/A/B/A/B where A has the global file disabled and B the canonical file enabled, a worker-generation barrier after each switch, 20 discarded and 50 recorded healthy requests per block (200 records per mode, same endpoint, body and time-to-first-byte metric), and the PRD's gate `median(B) ≤ median(A) × 1.05` over all recorded values. Every block median and the aggregate are printed (`perf-result.txt`), all records are kept, no healthy request may reach `backsoon`, and the run ends with the canonical file enabled. There is no configurable threshold, skip flag or retry.

Recorded local acceptance: 111 checks passed. An independent isolated functional-only probe passed 101 checks and audited, without re-measuring, all 200 records per mode: A median 484 µs, B median 474.5 µs, delta −1.9628%, within the 5% gate. All blocks and the earlier failed +5.47% serial dataset remain archived; this does not establish a universal speedup. Manual browser recovery timing (CA-2) is still unverified.

## Checklist

- [ ] `docker compose -f compose.test.yml config --quiet` and individual shell checks (for example `make lint`) pass
- [ ] `up` ends with four `ok` probes
- [ ] `docker ps --filter label=com.backsoon.test=true` shows ports only on `127.0.0.1`
- [ ] `down` leaves no `backsoon-e2e` resources behind
