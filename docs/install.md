**English** | [Español](install.es.md)

# Installing backsoon

A guide for self-hosters who already run [Nginx Proxy Manager](https://nginxproxymanager.com/) (NPM) in Docker. It opens with one short path for the common case; everything else is in the [Reference](#reference) after it.

## Quick path: Portainer Git stack

Portainer deploys backsoon from this repository, and the stack installs the NPM fallback for you.

### What you need

- A Docker host with Portainer.
- NPM running in a container, with its data folder bound to the host (a bind mount, not a `tmpfs`).
- A DNS name for the panel, for example `backsoon.example.com`, pointing to NPM.

### 1. Find three facts about your NPM

Run these on the Docker host. Replace `<npm>` with NPM's container name.

| Fact | Command | What to note |
|------|---------|--------------|
| The Docker network NPM shares | `docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' <npm>` | The name of the network NPM shares with its applications (not its database network), for example `npm-nw`. |
| Its address range | `docker network inspect <network> --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'` | A range such as `172.18.0.0/16`. |
| NPM's data folder on the host | `docker inspect -f '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}' <npm>` | The path on the line that ends in `-> /data`, for example `/docker/nginx-proxy-manager/data`. Add `/nginx/custom` to it. |

If NPM has no network of its own to share, create one and attach NPM: `docker network create proxy && docker network connect proxy <npm>`. Then use `proxy` as the network name. [Step 1 of the Reference](#step-1-the-proxy-network-with-npm-attached) explains how to make that attachment permanent.

### 2. Generate the password hash and the session secret

```sh
docker run --rm httpd:2.4-alpine htpasswd -nbBC 12 admin 'your-password' | cut -d: -f2
openssl rand -hex 32
```

The first command prints the bcrypt hash of your password (it starts with `$2y$12$`). Start the line with a space so your shell does not keep the password in its history. The second prints a random secret. Keep both for the next step.

### 3. Set the variables

Copy this block and fill in the values. In Portainer, add each line under *Environment variables*, or save the block as a file and use *Load variables from .env file*.

```sh
# The Docker network that NPM shares (fact 1). The default is "proxy".
BACKSOON_PROXY_NETWORK=npm-nw
# NPM's address range (fact 2). Only this network may tell the panel a visitor's address.
BACKSOON_TRUSTED_PROXIES=172.18.0.0/16
# NPM's data folder on the host (fact 3) plus /nginx/custom. Every deploy installs the fallback there.
BACKSOON_NPM_CUSTOM_DIR=/docker/nginx-proxy-manager/data/nginx/custom
# The user name you will sign in to the panel with.
BACKSOON_ADMIN_USER=admin
# The bcrypt hash from step 2, not the password itself.
BACKSOON_ADMIN_PASSWORD_HASH=$2y$12$paste-your-hash-here
# The random secret from step 2. It signs the sign-in sessions.
BACKSOON_SESSION_SECRET=paste-your-secret-here
```

About the hash: paste it as it is. If the panel then refuses to start with `BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`, enter it again with every `$` written as `$$` (Compose reads a single `$` as the start of a variable and cuts the hash).

Optional variables, none needed to start: `BACKSOON_REFRESH` (seconds between automatic retries, default `10`), `BACKSOON_NPM_INTERCEPT_503` (`false` leaves an application's own `503` responses alone), `BACKSOON_ADMIN_HOST_BIND` and `BACKSOON_ADMIN_HOST_PORT` (where the panel is published on the host, default `127.0.0.1:8181`). All of them are described in the README's [Configuration](../README.md#configuration).

### 4. Deploy the stack

In Portainer: *Stacks → Add stack → Repository*. Set this repository's URL and branch, keep the compose path `docker-compose.yml`, add the variables from step 3, and deploy. The first build takes a few minutes.

Then check the containers on the Docker host:

```sh
docker ps -a --filter name=backsoon --format '{{.Names}}: {{.Status}}'
docker logs backsoon-npm-snippet
```

`backsoon` and `backsoon-admin` should be `healthy`, `backsoon-init` and `backsoon-npm-snippet` should be `Exited (0)`, and the second command should say `installed`, followed by `ACTION NEEDED: reload Nginx Proxy Manager…` (step 5 does that reload); on a later redeploy it says `unchanged; no reload needed`. If `backsoon-admin` keeps restarting, `docker logs backsoon-admin` names the variable that is missing or invalid.

### 5. Create the panel's proxy host

In NPM, add a **proxy host**. **Saving it is what activates the fallback**: NPM reads the file that the stack installed at its next reload, and saving a host reloads it.

| Field | Value |
|-------|-------|
| Domain | `backsoon.example.com` (your panel's DNS name) |
| Scheme | `http` |
| Forward hostname | `backsoon-admin` |
| Forward port | `8080` |
| SSL | request or select a certificate, and turn on **Force SSL** |
| Advanced | empty |

Then open `https://backsoon.example.com` and sign in with `BACKSOON_ADMIN_USER` and the password you hashed. That is the panel's owner. The panel signs in only over HTTPS. To let others manage pages, the owner adds them under **Users** (see [Users and roles](../README.md#users-and-roles)).

### 6. Check that it works

1. Stop one of your applications: `docker stop <application>`.
2. Open its address in a browser. You should see a maintenance page instead of NPM's `502 Bad Gateway`.
3. Start it again: `docker start <application>`. The maintenance page retries by itself every 10 seconds (`BACKSOON_REFRESH`), so the application should come back within about 10 seconds. That interval is the design value; the recovery time in a real browser has not been measured (see [What has been verified](#what-has-been-verified)).

No page of yours exists yet, so visitors see the provisional default page. In the panel, create a page and give it the application's domain as a destination.

If something does not match, see [Verification](#verification) for the exact `curl` checks and `docker logs` for each container.

## Reference

Everything below is optional reading: the other ways to install, the details behind the quick path, and what has been verified.

### The installation in four steps

The full installation, with every option, is **4 steps**:

| Step | What | Where |
|------|------|-------|
| 1 | Create the `proxy` network and attach NPM to it | Docker host |
| 2 | Deploy the stack (`backsoon-init`, `backsoon`, `backsoon-admin`) | Portainer or `docker compose` |
| 3 | Install `npm/server_proxy.conf` in NPM | NPM container |
| 4 | Publish the panel through NPM, log in and create pages | NPM UI, then the panel |

Then [verify](#verification). To undo everything, see [Rollback](#rollback). Portainer and plain `docker compose` use the same commands; only the way you start the stack differs.

### Prerequisites

- A Docker host with Docker Compose v2 (checked with Docker 29.9 and Compose 5.6) that you can reach with `docker`, plus internet access for the first build.
- A running NPM container. The tested versions are `jc21/nginx-proxy-manager:2.16.0` and `2.15.1` (both end-to-end suites); other versions are untested (see [After updating NPM](#after-updating-npm)).
- DNS records for the panel (for example `backsoon.example.com`) and for your applications that point to NPM.
- Nothing is pulled from a registry for backsoon itself: the stack **builds** its two images from this repository (`nginx/Dockerfile` and `admin/Dockerfile`, build context = repository root) and tags them `backsoon:local` and `backsoon-admin:local`. The first build downloads three pinned base images (`nginx`, `golang`, `distroless`) and takes a few minutes.

#### What gets created

| Kind | Name | Notes |
|------|------|-------|
| Containers | `backsoon-init`, `backsoon`, `backsoon-admin`, `backsoon-npm-snippet` | Fixed `container_name`s, so a host can run only one backsoon stack. `backsoon-init` and `backsoon-npm-snippet` are one-shots that exit with code 0. |
| Volumes | `backsoon_pages`, `backsoon_meta`, `backsoon_npm_custom` | Fixed names. `backsoon_pages` holds the served pages; `backsoon_meta` holds the panel's `sites.json`, `users.json` (the added users, as bcrypt hashes) and original HTML; `backsoon_npm_custom` stays empty (it stands in for NPM's directory when `BACKSOON_NPM_CUSTOM_DIR` is unset). |
| Network | `proxy` (external, or `BACKSOON_PROXY_NETWORK`) | Must exist before the deploy. |
| Ports | `127.0.0.1:8181` → panel `8080` | `BACKSOON_ADMIN_HOST_BIND` and `BACKSOON_ADMIN_HOST_PORT`. `backsoon` publishes nothing. |

`backsoon-init` seeds the provisional `default.html` into `backsoon_pages` (it never overwrites an existing one) and hands the volume to uid 65532, the panel's user, on every deploy. `backsoon` and `backsoon-admin` wait for it to finish successfully. The panel listens on port 8080 inside the network; `backsoon` listens on port 80.

### Step 1: the `proxy` network, with NPM attached

If NPM already shares a user-defined network that you want backsoon to join, use it instead of creating `proxy`: set `BACKSOON_PROXY_NETWORK` to its name in step 2a and skip the rest of this step. List NPM's networks with:

```sh
docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' <npm>
```

Pick a network whose other members you trust (ideally NPM alone), not NPM's database network. Otherwise, check whether the `proxy` network already exists, and create it if not:

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

### Step 2: deploy the stack

#### 2a. Prepare the variables

Copy [`env.example`](../env.example) to a private `.env` (it is gitignored) and fill in:

- `BACKSOON_ADMIN_USER`: the panel owner's user name. Other users are added in the panel.
- `BACKSOON_ADMIN_PASSWORD_HASH`: a bcrypt hash of the password.
- `BACKSOON_SESSION_SECRET`: a random string of at least 32 bytes.

Generate the hash and the secret, and enter the hash with the quoting that keeps its `$` characters intact, by following steps 1 to 3 of [Admin panel](../README.md#admin-panel) in the README. Without a user, a password or hash, and a secret, the panel refuses to start and names the missing variable; `backsoon` keeps serving either way. The remaining variables are optional: `BACKSOON_REFRESH` (default `10`), `BACKSOON_PROXY_NETWORK` (default `proxy`; the network from step 1) and `BACKSOON_TRUSTED_PROXIES`. Set the latter to the range of that network so that only NPM's network can set the client address of the login rate limit:

```sh
docker network inspect proxy --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'
```

(replace `proxy` with your `BACKSOON_PROXY_NETWORK`).

For example `BACKSOON_TRUSTED_PROXIES=172.18.0.0/16`. At start the panel logs `login rate limit: X-Real-IP trusted from 172.18.0.0/16`; an invalid value stops it with a message naming the variable.

#### 2b. Start the stack

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

#### 2c. Check it

```sh
docker compose ps -a      # backsoon and backsoon-admin: healthy; backsoon-init: Exited (0)
docker exec backsoon wget -qSO- http://127.0.0.1/ 2>&1 | grep 503
docker exec <npm> curl -sI http://backsoon/
```

The second command prints the `503` status line (`backsoon` answers 503 with the provisional default page by design). It uses `127.0.0.1`, not `localhost`: inside the container `localhost` resolves to `::1` first and `backsoon` listens on IPv4 only, so `localhost` is refused. The container's `wget` also prints only the status line of an error response, so the headers come from the third command: run from NPM, it proves the shared network and shows `Retry-After`, `Cache-Control: no-store` and `X-Backsoon: 1`.

If `backsoon-admin` is restarting, read `docker logs backsoon-admin`: it names the invalid or missing variable.

### Step 3: install `server_proxy.conf` in NPM

Decide first whether any of your applications legitimately answers `503` itself. The shipped file intercepts `502`, `503` and `504`. To leave application `503` responses alone, use the variant whose one business line is `error_page 502 504 =503 @backsoon;`.

#### Automatic (recommended)

Set `BACKSOON_NPM_CUSTOM_DIR` to the **host** path of NPM's `/data/nginx/custom` directory (for NPM with `/docker/nginx-proxy-manager/data` bound to `/data`: `/docker/nginx-proxy-manager/data/nginx/custom`), and `BACKSOON_NPM_INTERCEPT_503=false` for the variant. On every deploy the one-shot `backsoon-npm-snippet` service writes the file atomically (`docker logs backsoon-npm-snippet` says `installed` or `unchanged`); it refuses, and changes nothing, if a `server_proxy.conf` that does not route to `@backsoon` is already there. `backsoon` and the panel wait for it, so a refusal stops the deploy, like any other configuration error: containers that the deploy recreates (always on the first deploy of a new version) stay stopped until you fix the variable or move the foreign file and redeploy. The file belongs to backsoon: every deploy replaces edits made to it, so choose the variant with `BACKSOON_NPM_INTERCEPT_503` instead of editing it. Check the path: a mistyped `BACKSOON_NPM_CUSTOM_DIR` makes Docker create an empty directory that NPM never reads. If NPM runs as a non-root user (`PUID`/`PGID`) and owns that directory, the installer, which has no capabilities, cannot write there and says so. It cannot reload NPM: NPM applies the file at its next reload, which happens when you save any proxy host (creating the panel's host in step 4 is enough) or restart NPM. To check or force it now: `docker exec <npm> nginx -t && docker exec <npm> nginx -s reload`.

#### By hand

Install the file, validate and reload (run the optional `sed` line between the copy and the rename for the variant):

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

### Step 4: publish the panel, log in, create pages

1. In NPM, add a **proxy host** with these values (the labels may differ slightly between NPM versions). Forward by container name over the shared network, as below, not to the panel's published host port: the fallback needs that network anyway (`backsoon:80`), and through the host port NPM's address falls outside `BACKSOON_TRUSTED_PROXIES`, so all visitors would share one login-limit counter:

   | Field | Value |
   |-------|-------|
   | Domain | `backsoon.example.com` (yours) |
   | Scheme | `http` |
   | Forward hostname | `backsoon-admin` |
   | Forward port | `8080` |
   | SSL | request or select a certificate and force SSL |
   | Advanced | empty |

   The panel's session cookie is `Secure`, so the login only works over HTTPS (or on `http://localhost`, for example through an SSH tunnel to the published `127.0.0.1:8181`). Keep the NPM Access List or a LAN/VPN-only host in mind for an extra layer.
2. Repeat `nginx -t && nginx -s reload` in NPM if this is your first proxy host (see step 3).
3. Open `https://backsoon.example.com`, log in with `BACKSOON_ADMIN_USER` and the password you hashed. To add users who manage pages, open **Users** (owner only; see [Users and roles](../README.md#users-and-roles)).
4. Create a page from the template (or upload your own `.html`) and give it its **destinations**: the application's domain (`app1.example.com`) or the `service:port` NPM forwards to (`app1:3000`). The first match wins in the order domain, then `service:port`, then the default page. Changes apply to the next request, with no restart.

### Verification

With the application stopped (or use the safe variant below):

```sh
curl -sI https://app1.example.com
# HTTP/… 503, Retry-After: 10, Cache-Control: no-store, X-Backsoon: 1, Content-Security-Policy, X-Content-Type-Options: nosniff (each exactly once)
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

### When `backsoon` or the panel is stopped

| Stopped | Result |
|---------|--------|
| `backsoon` | NPM starts, reloads and serves every healthy application. A stopped application gets NPM's own generic `502` (no maintenance headers or body), not a hang. |
| `backsoon-admin` (the panel) | `backsoon` keeps serving all pages. The panel's own domain answers with the default maintenance page (`503`) until it is back. |
| an application | Its domain shows its page (or the default), `503`. This is the point of backsoon. |

### Rollback

Disable the global file (nothing else changes: no proxy host, no stored page). With the automatic install, first remove `BACKSOON_NPM_CUSTOM_DIR` from the stack and redeploy; otherwise the next deploy installs the file again and NPM picks it up at its next reload. Then:

```sh
docker exec <npm> mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.off
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

NPM includes the file optionally, so the reload does not fail. The `backsoon` containers may stay up or be stopped; they have no effect without the file. Restore with `docker exec <npm> mv /data/nginx/custom/server_proxy.conf.off /data/nginx/custom/server_proxy.conf` and the same `nginx -t && nginx -s reload`. To remove the stack, delete it in Portainer or run `docker compose down`; add `-v` only if you also want to delete the pages and panel data (`backsoon_pages`, `backsoon_meta`, and the empty `backsoon_npm_custom`). Removing the stack does not remove an automatically installed `server_proxy.conf`: disable it as above, or restore NPM's default configuration first.

#### Restore NPM's default configuration (before removing the stack)

backsoon only ever adds `server_proxy.conf` to NPM's custom directory and refuses to overwrite one that is not its own, so NPM's previous state is simply that file being absent; no backup is needed. With the automatic install, set `BACKSOON_NPM_RESTORE=true` in the stack (keep `BACKSOON_NPM_CUSTOM_DIR`) and redeploy. `backsoon-npm-snippet` then removes the file instead of installing it, and `docker logs backsoon-npm-snippet` says `removed …: NPM is back to its default configuration`, or `is not present` if there was nothing to remove. It never removes a `server_proxy.conf` that does not route to `@backsoon`. Reload NPM (`docker exec <npm> nginx -t && docker exec <npm> nginx -s reload`, or save any proxy host), then remove the stack and, if you no longer need it, the panel's proxy host in NPM. As long as `BACKSOON_NPM_RESTORE=true` stays set, every deploy keeps the file away; set it back to `false` (or remove it) and redeploy to install it again.

### After updating NPM

```sh
docker exec <npm> nginx -t
```

Then repeat the checks in [Verification](#verification) (the maintenance page with a stopped application, and NPM starting with `backsoon` stopped), or run `BACKSOON_TEST_NPM_IMAGE=jc21/nginx-proxy-manager:<version> make e2e` against the new version (see [the harness guide](npm-harness.md)).

### Updating backsoon

Pull the repository changes and redeploy with a rebuild: `docker compose up -d --build`, or redeploy the Git stack in Portainer so that it pulls the repository again and rebuilds (the Portainer option names are not verified). Existing pages survive: seeding never overwrites `default.html`, and the panel republishes its pages from `backsoon_meta` at start. Upgrading from a version without panel users ends every session once: sign in again after the first start (users you add later are kept in `backsoon_meta` across updates).

### What has been verified

A clean-host rehearsal of these four steps, with an isolated network and a scratch NPM `2.16.0`, passed: build, `backsoon-init` hand-over, the checks above, the `server_proxy.conf` install, rollback and restore, the `502 504` variant, panel login and page creation through NPM, and CA-1 with a stopped application. The rehearsal did not cover:

- **TLS.** No certificate was issued; the panel was reached over HTTP with the `Secure` cookie sent by hand.
- **The real NPM and Portainer user interfaces.** The proxy hosts were created through NPM's API, and Portainer was not used; the Portainer steps follow Compose's behaviour, not a run in Portainer.
- **Production.** Real applications, real redeploys and the manual browser recovery timing (CA-2, CA-7).

The production deploy below covers the first two and part of the third.

#### Production (U8)

On the owner's server, deployed as a Portainer Git stack from `main` (`b706071`) with the automatic install:

- **Host.** Ubuntu 24.04 on `aarch64` (Oracle Ampere), Docker 29.8, Portainer EE, NPM `2.15.1` with its own network `npm-nw` (`BACKSOON_PROXY_NETWORK=npm-nw`, `BACKSOON_TRUSTED_PROXIES` set to that network's range). Sites behind Cloudflare.
- **Deploy and redeploy.** Both images built on the host; *Pull and redeploy* rebuilt them; `backsoon-init` and `backsoon-npm-snippet` exited with code 0 and the snippet log said `installed`; `backsoon` and `backsoon-admin` were healthy, with the panel published on `127.0.0.1:8181` only.
- **NPM.** The snippet became active at NPM's next reload (saving the panel's proxy host): responses carried `X-Backsoon: 1`. NPM includes it in every proxy host, existing and new.
- **Panel.** Reached through an NPM proxy host to `backsoon-admin:8080` with a Let's Encrypt certificate and Force SSL; sign-in and page creation worked.
- **Applications.** A real application stopped behind NPM showed its maintenance page, and the owner confirmed it working.

Not measured: the browser recovery time after an application comes back (CA-2).

### Differences from the PRD runbook (§13)

- The check command uses `127.0.0.1`, not `localhost`, and the NPM-side `curl` shows the headers.
- `mkdir -p /data/nginx/custom` is needed on a fresh NPM.
- The panel hash uses bcrypt cost 12 and the pinned `httpd:2.4-alpine` image, as in the README.
- The log trace is `502 : 503` (or `-, 503`), not `"502, 503"`.
