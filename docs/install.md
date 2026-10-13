# Installing backsoon

A step-by-step guide for self-hosters who already run [Nginx Proxy Manager](https://nginxproxymanager.com/) (NPM) in Docker. It uses Portainer or plain `docker compose`; the commands are the same, only the way you start the stack differs.

The installation is **4 steps**:

| Step | What | Where |
|------|------|-------|
| 1 | Create the `proxy` network and attach NPM to it | Docker host |
| 2 | Deploy the stack (`backsoon-init`, `backsoon`, `backsoon-admin`) | Portainer or `docker compose` |
| 3 | Install `npm/server_proxy.conf` in NPM | NPM container |
| 4 | Publish the panel through NPM, log in and create pages | NPM UI, then the panel |

Then [verify](#verification). To undo everything, see [Rollback](#rollback).

## Prerequisites

- A Docker host with Docker Compose v2 (checked with Docker 29.9 and Compose 5.6) that you can reach with `docker`, plus internet access for the first build.
- A running NPM container. The tested version is `jc21/nginx-proxy-manager:2.16.0`; other versions are untested (see [After updating NPM](#after-updating-npm)).
- DNS records for the panel (for example `backsoon.example.com`) and for your applications that point to NPM.
- Nothing is pulled from a registry for backsoon itself: the stack **builds** its two images from this repository (`nginx/Dockerfile` and `admin/Dockerfile`, build context = repository root) and tags them `backsoon:local` and `backsoon-admin:local`. The first build downloads three pinned base images (`nginx`, `golang`, `distroless`) and takes a few minutes.

### What gets created

| Kind | Name | Notes |
|------|------|-------|
| Containers | `backsoon-init`, `backsoon`, `backsoon-admin` | Fixed `container_name`s, so a host can run only one backsoon stack. `backsoon-init` is a one-shot that exits with code 0. |
| Volumes | `backsoon_pages`, `backsoon_meta` | Fixed names. `backsoon_pages` holds the served pages; `backsoon_meta` holds the panel's `sites.json` and original HTML. |
| Network | `proxy` (external) | Must exist before the deploy. The stack publishes **no ports**. |

`backsoon-init` seeds the provisional `default.html` into `backsoon_pages` (it never overwrites an existing one) and hands the volume to uid 65532, the panel's user, on every deploy. `backsoon` and `backsoon-admin` wait for it to finish successfully. The panel listens on port 8080 inside the network; `backsoon` listens on port 80.

## Step 1: the `proxy` network, with NPM attached

Check whether the network already exists, and create it if not:

```sh
docker network ls --filter name=^proxy$
docker network create proxy
```

Attach your NPM container to it:

```sh
docker network connect proxy <npm>
```

`<npm>` is NPM's container name. A manual `connect` lasts only as long as that container: make it permanent in NPM's own stack (in Portainer, edit the NPM stack) by adding the network to the NPM service and declaring it external:

```yaml
services:
  npm:                 # your existing NPM service
    networks:
      - default        # keep whatever networks it already has
      - proxy
networks:
  proxy:
    external: true
```

NPM reaches `backsoon` and `backsoon-admin` by container name over this network. Only attach containers you trust: any container on `proxy` can reach the panel (see [Admin panel](../README.md#admin-panel) in the README for the consequence for the login limiter).

## Step 2: deploy the stack

### 2a. Prepare the variables

Copy [`env.example`](../env.example) to a private `.env` (it is gitignored) and fill in:

- `BACKSOON_ADMIN_USER`: the panel user.
- `BACKSOON_ADMIN_PASSWORD_HASH`: a bcrypt hash of the password.
- `BACKSOON_SESSION_SECRET`: a random string of at least 32 bytes.

Generate the hash and the secret, and enter the hash with the quoting that keeps its `$` characters intact, by following steps 1 to 3 of [Admin panel](../README.md#admin-panel) in the README. Without a user, a password or hash, and a secret, the panel refuses to start and names the missing variable; `backsoon` keeps serving either way. The remaining variable, `BACKSOON_REFRESH`, is optional (default `10`).

### 2b. Start the stack

**Portainer, from a Git repository (recommended).** *Stacks → Add stack → Repository*: set this repository's URL and branch, keep the compose path `docker-compose.yml`, and add the variables from your `.env` under *Environment variables* (or *Load variables from .env file*). Portainer clones the repository, so the build contexts exist. If the hash is rejected with `BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`, enter it again with every `$` written as `$$`.

**Portainer, web editor.** The web editor deploys only the pasted compose file, without `nginx/`, `admin/` or `templates/`, so the build cannot run there. Build the images on the Docker host first, then paste `docker-compose.yml` into the editor; Compose uses the existing `backsoon:local` and `backsoon-admin:local` images and does not build:

```sh
git clone <repository-url> backsoon && cd backsoon
docker compose build
```

**Plain `docker compose`**, from the repository root (it reads `.env` from that directory):

```sh
git clone <repository-url> backsoon && cd backsoon
cp env.example .env        # edit it as in 2a
docker compose up -d --build
```

### 2c. Check it

```sh
docker compose ps -a      # backsoon and backsoon-admin: healthy; backsoon-init: Exited (0)
docker exec backsoon wget -qSO- http://127.0.0.1/ 2>&1 | grep 503
docker exec <npm> curl -sI http://backsoon/
```

The second command prints the `503` status line (`backsoon` answers 503 with the provisional default page by design). It uses `127.0.0.1`, not `localhost`: inside the container `localhost` resolves to `::1` first and `backsoon` listens on IPv4 only, so `localhost` is refused. The container's `wget` also prints only the status line of an error response, so the headers come from the third command: run from NPM, it proves the shared network and shows `Retry-After`, `Cache-Control: no-store` and `X-Backsoon: 1`.

If `backsoon-admin` is restarting, read `docker logs backsoon-admin`: it names the invalid or missing variable.

## Step 3: install `server_proxy.conf` in NPM

Decide first whether any of your applications legitimately answers `503` itself. The shipped file intercepts `502`, `503` and `504`. To leave application `503` responses alone, run the optional `sed` line below between the copy and the rename: it changes the one business line to `error_page 502 504 =503 @backsoon;` and nothing else.

Install the file, validate and reload:

```sh
docker exec <npm> mkdir -p /data/nginx/custom
docker cp npm/server_proxy.conf <npm>:/data/nginx/custom/server_proxy.conf.new
# optional, only for the legitimate-503 policy:
docker exec <npm> sed -i 's/^error_page 502 503 504 =503 @backsoon;/error_page 502 504 =503 @backsoon;/' /data/nginx/custom/server_proxy.conf.new
docker exec <npm> mv /data/nginx/custom/server_proxy.conf.new /data/nginx/custom/server_proxy.conf
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

- `mkdir -p` is needed on a fresh NPM, where `/data/nginx/custom` does not exist yet.
- Copying to `.new` and renaming makes the file appear atomically: NPM only includes the exact name `server_proxy.conf`.
- NPM includes the file inside each proxy host, so with **no proxy hosts yet** `nginx -t` does not exercise it. Repeat `nginx -t && nginx -s reload` after you create the first host in step 4.
- A reload is asynchronous: old workers finish their requests first. Wait a few seconds before you test.
- If `docker cp` is refused (`Could not find the file`), your `/data` is a `tmpfs`; use a volume or a bind mount.

Do not put anything in the proxy hosts' *Advanced* tab: the global file is the only configuration backsoon needs. [How it works and when `backsoon` is unavailable](../README.md#npm-global-configuration).

## Step 4: publish the panel, log in, create pages

1. In NPM, add a **proxy host** with these values (the labels may differ slightly between NPM versions):

   | Field | Value |
   |-------|-------|
   | Domain | `backsoon.example.com` (yours) |
   | Scheme | `http` |
   | Forward hostname | `backsoon-admin` |
   | Forward port | `8080` |
   | SSL | request or select a certificate and force SSL |
   | Advanced | empty |

   The panel's session cookie is `Secure`, so the login only works over HTTPS. Keep the NPM Access List or a LAN/VPN-only host in mind for an extra layer.
2. Repeat `nginx -t && nginx -s reload` in NPM if this is your first proxy host (see step 3).
3. Open `https://backsoon.example.com`, log in with `BACKSOON_ADMIN_USER` and the password you hashed.
4. Create a page from the template (or upload your own `.html`) and give it its **destinations**: the application's domain (`app1.example.com`) or the `service:port` NPM forwards to (`app1:3000`). The first match wins in the order domain, then `service:port`, then the default page. Changes apply to the next request, with no restart.

## Verification

With the application stopped (or use the safe variant below):

```sh
curl -sI https://app1.example.com
# HTTP/… 503, Retry-After: 10, Cache-Control: no-store, X-Backsoon: 1 (each exactly once)
curl -sI -X POST https://app1.example.com                          # 503, not 405
curl -sI -H 'Accept: application/json' https://app1.example.com    # Content-Type: application/json
curl -s  -H 'Accept: application/json' https://app1.example.com    # {"status":"maintenance","retry_after":10}
```

Without DNS yet, add `-H 'Host: app1.example.com'` and point curl at NPM's address over plain HTTP.

**Safe variant, without stopping a real application.** Add a temporary proxy host `test.example.com` forwarding to `backsoon:9` (a closed port): the connection is refused and the request must get the `503` maintenance page. Delete the host afterwards.

**NPM restarts with `backsoon` stopped.** `docker stop backsoon && docker restart <npm>`: every healthy application must still respond. Start `backsoon` again afterwards.

**Count the maintenance pages served.** NPM logs the controlled fallback as `<upstream status> : 503` in the upstream field:

```sh
docker exec <npm> sh -c "grep -hE ' ([0-9]{3}|-)( :|,) 503 503 ' /data/logs/proxy-host-*_access.log"
```

A refused connection from the application logs `502 : 503`. A stopped container that no longer resolves in Docker DNS logs `-, 503`. An application that returns its own `503` (default policy) logs `503 : 503`; with the `502 504` variant it logs plain `503 503` and is not matched. This is not the `"502, 503"` of the PRD's runbook (§13, RNF-9): NPM 2.16 prints `502 : 503`, so a grep for the quoted string finds nothing.

## When `backsoon` or the panel is stopped

| Stopped | Result |
|---------|--------|
| `backsoon` | NPM starts, reloads and serves every healthy application. A stopped application gets NPM's own generic `502` (no maintenance headers or body), not a hang. |
| `backsoon-admin` (the panel) | `backsoon` keeps serving all pages. The panel's own domain answers with the default maintenance page (`503`) until it is back. |
| an application | Its domain shows its page (or the default), `503`. This is the point of backsoon. |

## Rollback

Disable the global file (nothing else changes: no proxy host, no stored page):

```sh
docker exec <npm> mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.off
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

NPM includes the file optionally, so the reload does not fail. The `backsoon` containers may stay up or be stopped; they have no effect without the file. Restore with `docker exec <npm> mv /data/nginx/custom/server_proxy.conf.off /data/nginx/custom/server_proxy.conf` and the same `nginx -t && nginx -s reload`. To remove the stack, delete it in Portainer or run `docker compose down`; add `-v` only if you also want to delete the pages and panel data (`backsoon_pages`, `backsoon_meta`).

## After updating NPM

```sh
docker exec <npm> nginx -t
```

Then repeat the checks in [Verification](#verification) (the maintenance page with a stopped application, and NPM starting with `backsoon` stopped), or run `make e2e` after setting the new image version in `compose.test.yml` (see [the harness guide](npm-harness.md)).

## Updating backsoon

Pull the repository changes and redeploy with a rebuild: `docker compose up -d --build`, or redeploy the Git stack in Portainer so that it pulls the repository again and rebuilds (the Portainer option names are not verified). Existing pages survive: seeding never overwrites `default.html`, and the panel republishes its pages from `backsoon_meta` at start.

## What has been verified

A clean-host rehearsal of these four steps, with an isolated network and a scratch NPM `2.16.0`, passed: build, `backsoon-init` hand-over, the checks above, the `server_proxy.conf` install, rollback and restore, the `502 504` variant, panel login and page creation through NPM, and CA-1 with a stopped application. The rehearsal did not cover:

- **TLS.** No certificate was issued; the panel was reached over HTTP with the `Secure` cookie sent by hand.
- **The real NPM and Portainer user interfaces.** The proxy hosts were created through NPM's API, and Portainer was not used; the Portainer steps follow Compose's behaviour, not a run in Portainer.
- **Production.** Real applications, real redeploys and the manual browser recovery timing (CA-2, CA-7) are still to be validated on the owner's servers (U8).

## Differences from the PRD runbook (§13)

- The check command uses `127.0.0.1`, not `localhost`, and the NPM-side `curl` shows the headers.
- `mkdir -p /data/nginx/custom` is needed on a fresh NPM.
- The panel hash uses bcrypt cost 12 and the pinned `httpd:2.4-alpine` image, as in the README.
- The log trace is `502 : 503` (or `-, 503`), not `"502, 503"`.
