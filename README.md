# backsoon

A friendly maintenance page for apps behind Nginx Proxy Manager (NPM). When an upstream is down, NPM hands the request to `backsoon`, a static nginx that answers `503` with the right page for that domain or destination. Design and requirements live in [PRD.md](PRD.md).

> **Status: walking skeleton.** Only the static `backsoon` service and the repository foundation exist. The NPM snippet (`npm/server_proxy.conf`), the admin panel and the full installation guide are not implemented yet.

| Piece | State |
|-------|-------|
| `backsoon` static nginx service (`nginx/`, `templates/`, `docker-compose.yml`) | implemented |
| Local NPM test harness (`compose.test.yml`, [docs/npm-harness.md](docs/npm-harness.md)) | separate unit, see its doc |
| `npm/server_proxy.conf` and `make e2e` (`test/e2e.sh`) | **not yet** (NPM integration) |
| `backsoon-admin` panel (`admin/` only has `go.mod`) | **not yet** |
| CI, bcrypt hash guide, the `error_page 502 504` variant (RF-10) | **not yet** |

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
| `make e2e` | Runs `test/e2e.sh`, which does not exist yet (NPM integration). |

`make test` has no Go part yet: `admin/` has no packages, so `go test` joins in with the panel.

## Details

| Topic | Decision |
|-------|----------|
| Image | `nginx:1.30.5-alpine-slim`, pinned by digest in `nginx/Dockerfile`. Its entrypoint ships `envsubst`, so the plain `nginx:alpine` fallback is not needed. |
| Config rendering | `nginx/default.conf.template` is rendered by the official entrypoint. `NGINX_ENVSUBST_FILTER` limits substitution to `${BACKSOON_REFRESH}`, so nginx's own `${...}` captures survive. |
| Runtime | `worker_processes 1` in `nginx/nginx.conf`; nginx is the only process. |
| Logs | `host target status file` per request, no client address. Pages with no file log the empty-response helper URI. |
| Build context | Repository root, so the image reuses `templates/maintenance.html`. |

## Deferred validation

`make lint` skips the NPM snippet check while `npm/server_proxy.conf` is missing and prints a `skip` line; once the file exists it is wrapped in a test `server{}` and run through `nginx -t` with the service image's nginx (not NPM's own). Checks against a real NPM, the panel half of CA-14/CA-16 and the manual browser checks are not covered yet.
