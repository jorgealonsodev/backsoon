# backsoon

A friendly maintenance page for apps behind Nginx Proxy Manager (NPM). When an upstream is down, NPM hands the request to `backsoon`, a static nginx that answers `503` with the right page for that domain or destination. Design and requirements live in [PRD.md](PRD.md).

> **Status: walking skeleton.** Static `backsoon` is verified. The NPM global snippet passed local end-to-end acceptance and independent functional verification. The owner explicitly left this candidate without native review after a pre-authority tooling failure; this is not native approval. No production deployment or merge has been performed. The admin panel, CI and full Portainer installation guide remain outside this first cut.

| Piece | State |
|-------|-------|
| `backsoon` static nginx service (`nginx/`, `templates/`, `docker-compose.yml`) | implemented |
| Local NPM test harness (`compose.test.yml`, [docs/npm-harness.md](docs/npm-harness.md)) | separate unit, see its doc |
| `npm/server_proxy.conf` and `make e2e` (`test/e2e.sh`) | local acceptance and independent functional checks passed; owner-authorized native-review omission |
| `backsoon-admin` panel (`admin/` only has `go.mod`) | **not yet** |
| CI and bcrypt hash guide | **not yet** |

## Quick path: try the service on its own

Needs Docker. Pick any free loopback port; the volume name below is throwaway.

```sh
docker build -t backsoon:try -f nginx/Dockerfile .
docker volume create backsoon_try_pages
docker run --rm -v backsoon_try_pages:/pages --entrypoint sh backsoon:try /docker-entrypoint.d/45-backsoon-seed.sh
docker run -d --name backsoon-try -v backsoon_try_pages:/pages:ro -p 127.0.0.1:8088:80 backsoon:try
curl -si http://127.0.0.1:8088/any/path      # 503 + Retry-After + Cache-Control: no-store + X-Backsoon: 1
curl -si -H 'Accept: application/json' http://127.0.0.1:8088/   # 503 + {"status":"maintenance","retry_after":10}
```

Clean up with `docker rm -f backsoon-try && docker volume rm backsoon_try_pages`.

Production uses the same pieces through `docker-compose.yml`: a one-shot `backsoon-init` seeds the volume, then `backsoon` serves it read-only on the external `proxy` network, with no published ports (`docker network create proxy` first, and attach NPM to it).

## How a page is chosen

| Order | Source | File in the `backsoon_pages` volume |
|-------|--------|--------------------------------------|
| 1 | `Host` header (lowercase, no port) | `/pages/host/<host>.html` |
| 2 | `X-Backsoon-Target: service:port` (sent by NPM) | `/pages/target/<service>_<port>.html` |
| 3 | default | `/pages/default.html` |
| 4 | nothing exists | `503` with an empty body (never `404`) |

Invalid hosts or targets never reach the filesystem: they fall back to the default page (nginx itself answers `400` to hosts containing `/`). Pages are read on every request, so adding, changing or removing a file takes effect immediately.

## Configuration

Copy [`env.example`](env.example) to `.env` and edit the private copy. In Portainer, enter the values under **Environment variables** or use **Load variables from .env file** to upload it. Never commit real credentials: `.env` and `.env.*` files are ignored; the public example contains no secrets.

The example lists all five variables from the PRD. Only `BACKSOON_REFRESH` is consumed by the current compose. The admin user, password/hash and session secret are reserved for the **not-yet-implemented panel** and are not passed to any current service. Its secrets are deliberately blank; importing the template does not enable an admin panel or configure authentication.

| Variable | Default | Meaning |
|----------|---------|---------|
| `BACKSOON_REFRESH` | `10` | Seconds used for `Retry-After`, the JSON `retry_after` and the page's `<meta http-equiv="refresh">`. Integer 1-99999; anything else stops the container at start. |

The provisional `default.html` is rendered from `templates/maintenance.html` by `backsoon-init`. If `default.html` already exists, seeding changes nothing, so redeploys keep your pages (and an old refresh value inside an existing page). Concurrent initializers render into exclusive temporary files on that volume and publish with a non-overwriting hard link; a losing initializer cannot modify the published page.

## Commands

| Command | What it does |
|---------|--------------|
| `make lint` | `docker compose config`, `nginx -t` on the rendered config, pinned-image and self-contained-page checks, shell syntax. |
| `make test` | Builds the image and runs `test/unit.sh`: curl tests against throwaway containers on loopback ephemeral ports (`bs-unit-*` names, removed on exit), including concurrent seeding. |
| `sh test/seed-race.sh` | Focused concurrent-seeding regression with two real init entrypoints and a read-only HTTP reader; checks publication, no overwrite and cleanup. |
| `make e2e-up` / `make e2e-down` | Start / remove the NPM harness. See [docs/npm-harness.md](docs/npm-harness.md). |
| `make e2e` | Disposable real NPM tests: healthy routing, host/target/default fallback, POST/HEAD/JSON, 502/503/504, absent backend/restart/recovery, rollback and legitimate-503 variant. |

`make test` has no Go part yet: `admin/` has no packages, so `go test` joins in with the panel.

## Details

| Topic | Decision |
|-------|----------|
| Image | `nginx:1.30.5-alpine-slim`, pinned by digest in `nginx/Dockerfile`. Its entrypoint ships `envsubst`, so the plain `nginx:alpine` fallback is not needed. |
| Config rendering | `nginx/default.conf.template` is rendered by the official entrypoint. `NGINX_ENVSUBST_FILTER` limits substitution to `${BACKSOON_REFRESH}`, so nginx's own `${...}` captures survive. |
| Runtime | `worker_processes 1` in `nginx/nginx.conf`; nginx is the only process. |
| Logs | `host target status file` per request, no client address. Pages with no file log the empty-response helper URI. |
| Build context | Repository root, so the image reuses `templates/maintenance.html`. |

## NPM global configuration

`npm/server_proxy.conf` is the only NPM configuration file needed. NPM must share the external `proxy` network with `backsoon`. Each proxy host keeps its normal upstream and **empty advanced configuration**.

After choosing the interception policy, install the file in your NPM container and validate before reloading:

```sh
docker cp npm/server_proxy.conf <npm>:/data/nginx/custom/server_proxy.conf
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

The named fallback passes the original host, the `Accept` header and NPM's `$server:$port` to `backsoon`. Variable `proxy_pass` defers DNS resolution, and interception is disabled inside the fallback so the 503 from `backsoon` is not re-intercepted. The internal request to `backsoon` always targets the fixed URI `/` (the trailing `/` in the variable URL), whatever the application URI was, so an application path such as `/healthz` or `/__backsoon` can never reach `backsoon`'s private locations; the browser URL does not change and nothing is redirected. Only `backsoon` adds maintenance headers.

### When `backsoon` is unavailable

Bounded degradation: only the fallback gets short timeouts (2 s resolving, 2 s connecting, 5 s of inactivity per send/read, one attempt); your applications keep NPM's own timeouts. If `backsoon` is stopped, missing or unreachable, the fallback fails within that bound and NPM answers with its native generic `502` (no maintenance headers or body).

nginx normally stops after the first `error_page` jump (`recursive_error_pages off`), so a failed fallback would end as a slow `504` or a hang on a stale cached address. The file therefore enables `recursive_error_pages` and fixes the chain to `server → @backsoon → @backsoon_unavailable`. `@backsoon` declares its own `error_page 502 504` list pointing to `@backsoon_unavailable`, which returns `502` and declares its own inert list. Each own list replaces the inherited one, so no hop can return to `@backsoon`. The one editable business list remains the only `error_page … @backsoon;` line.

NPM's request log shows a controlled HTTP `502` from an application as `upstream_status` `502 : 503` with client status `503` (observed on NPM 2.16.0 with the test fixture). A DNS-resolution failure of the application can log `-, 503` instead, so do not assume one format for every `502` cause.

### Applications with legitimate 503 responses

Edit only the single business `error_page … @backsoon;` line in the copied global file to exclude 503:

```nginx
error_page 502 504 =503 @backsoon;
```

Validate and reload NPM again (nginx applies a reload asynchronously: old workers keep serving until they exit). Your application then keeps its own 503 body and headers; 502/504 still use maintenance. Choose this policy before installing globally, since an application-generated 503 is otherwise intercepted.

### Disable safely

```sh
docker exec <npm> mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.disabled
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

No proxy host or stored page is changed. Copy the repository file back and reload to enable it again.

## Verification and remaining scope

```sh
make lint
make test
BACKSOON_TEST_HTTP_PORT=18080 BACKSOON_TEST_ADMIN_PORT=18181 make e2e
```

Use free loopback ports. `make e2e` requires a fresh `backsoon-*` project, seeds only test fixtures, and removes its containers/volumes on exit. It uses the same Dockerfile/config/entrypoint as production and a deterministic private upstream fixture. See [the harness guide](docs/npm-harness.md).

`make lint` wraps the NPM snippet in a syntax-only `server{}`; `make e2e` also checks actual NPM nginx syntax and dynamic recovery.

`make e2e` is one fixed procedure with no threshold or skip option. Functional checks run first; if any fails, the run exits failed and the performance step is skipped. Every reload (install, variant, rollback, absent backend) waits for the previous NPM worker generation to retire, because `nginx -T` or a first successful response does not prove all workers use the new configuration. Before the missing-backend check, a disposable proxy host points at the live test fixture on a closed port (immediate connection refusal), so the stopped `app1` is never queried while warming; an explicit reload (with the generation barrier) gives fresh workers with a cold `app1` cache. Then 150 fixed requests through that host must each return maintenance 503 with `X-Backsoon: 1` and the page, which makes every current NPM worker use and cache the `backsoon` address. Each worker must show the connection refusal (`connect() failed (111`) for that host in NPM's own error log; if a request fails, a worker is not covered or the window is exceeded, the run records curl's exit status/stderr and the NPM logs and fails before that check instead of claiming it. The single request that follows keeps the original 10 s client limit.

RNF-5 uses eight alternating blocks A/B/A/B/A/B/A/B (A: global file disabled, B: canonical file enabled), a worker-generation barrier at each switch, 20 discarded and 50 recorded healthy requests per block (200 records per mode), and the PRD's gate `median(B) ≤ median(A) × 1.05` over all records. All block medians and the aggregate are printed, all samples are kept under gitignored `.odd-checks/`, no healthy request may add a `backsoon` access-log line, and the run ends with the canonical file enabled. It is evaluated once per run, not retried until it passes. The recorded local acceptance passed 111 checks; a separate isolated functional-only verification passed 101 checks without repeating the benchmark. All eight blocks gave aggregate medians of 484 µs (A) and 474.5 µs (B), a −1.9628% difference, satisfying the 5% gate for that run. This is not evidence of a universal speedup. The earlier serial run's +5.47% failure is retained, not hidden.

The panel and its publication/API/security checks, CI, a full Portainer installation, and real production redeploy checks remain deferred to U4–U8. Static-page browser checks cover 320/1280 px in light/dark mode; the manual browser recovery timing in CA-2 has not been verified. TLS and physical-device testing are not part of the local NPM harness.
