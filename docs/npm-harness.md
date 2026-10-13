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
| Services | `npm`, `app1`, `app2` (always); `backsoon` (profile `backsoon`, start with `BACKSOON_TEST_WITH_BACKSOON=1`); `fixtures` (profile `tools`) |
| Proxy hosts | `app1.test → app1:80`, `app2.test → app2:80`, `advanced_config` empty |
| NPM user | `admin@backsoon.test` / built-in password, **test-only** (`BACKSOON_TEST_NPM_EMAIL`, `BACKSOON_TEST_NPM_PASSWORD`) |
| Volumes | `npm-data`, `npm-letsencrypt`, `npm-custom` (→ `/data/nginx/custom`, writable), `pages` |
| Labels | every container, network and volume has `com.backsoon.test=true` |
| Commands | `up`, `down`, `status`, `check`, `bootstrap`, `fixtures`, `compose …`, `help` |

For `test/e2e.sh`: run compose through `sh test/harness.sh compose <args>` (right file, project and profiles). Examples: `compose stop app1`, `compose cp npm/server_proxy.conf npm:/data/nginx/custom/`, `compose exec npm nginx -s reload`.

### Pages and the custom nginx directory

- `pages` is written only by `sh test/harness.sh fixtures` (clears the volume, copies `test/fixtures/pages/`). `backsoon` mounts it read-only at `/pages`.
- `npm-custom` is a separate volume, so `server_proxy.conf` can be installed or reset without editing project sources.
- `backsoon` needs `nginx/default.conf.template`; point elsewhere with `BACKSOON_TEST_TEMPLATE`. A missing template fails the start; it does not create a directory.

## Pinned images

| Image | Tag |
|-------|-----|
| `jc21/nginx-proxy-manager` | `2.16.0` |
| `traefik/whoami` | `v1.12.0` |
| `nginx` (optional `backsoon`) | `1.30.5-alpine-slim` |

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

## Checklist

- [ ] `docker compose -f compose.test.yml config --quiet` and `sh -n test/harness.sh test/npm-bootstrap.sh` pass
- [ ] `up` ends with four `ok` probes
- [ ] `docker ps --filter label=com.backsoon.test=true` shows ports only on `127.0.0.1`
- [ ] `down` leaves no `backsoon-e2e` resources behind
