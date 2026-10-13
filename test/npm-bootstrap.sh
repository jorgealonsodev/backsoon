#!/bin/sh
# Bootstrap the local test NPM through its public API (safe to re-run).
set -eu

usage() {
	cat <<'EOF'
Usage: sh test/npm-bootstrap.sh [domain=host:port ...]

Configures the TEST Nginx Proxy Manager started by test/harness.sh:
  1. waits (bounded) for GET /api/ to report status OK;
  2. if setup is false, creates the first (admin) user with POST /api/users;
  3. logs in with POST /api/tokens (token is kept in memory, never printed);
  4. creates or corrects each proxy host via /api/nginx/proxy-hosts
     (http, advanced_config empty). Default hosts:
       app1.test=app1:80  app2.test=app2:80

Environment (all optional):
  BACKSOON_TEST_ADMIN_PORT   loopback admin port        (default 8181)
  BACKSOON_TEST_NPM_URL      API base URL; loopback only (default http://127.0.0.1:$ADMIN_PORT)
  BACKSOON_TEST_NPM_EMAIL    test-only user email       (default admin@backsoon.test)
  BACKSOON_TEST_NPM_PASSWORD test-only user password    (default built-in, non-production)
  BACKSOON_TEST_WAIT         readiness timeout, seconds (default 120)

Requires python3 (stdlib only). Refuses non-loopback URLs so the test
credentials can never reach a real NPM.
EOF
}

case "${1:-}" in
-h | --help | help)
	usage
	exit 0
	;;
esac

command -v python3 >/dev/null 2>&1 || {
	echo "npm-bootstrap: python3 is required" >&2
	exit 2
}

BACKSOON_TEST_ADMIN_PORT=${BACKSOON_TEST_ADMIN_PORT:-8181}
BACKSOON_TEST_NPM_URL=${BACKSOON_TEST_NPM_URL:-http://127.0.0.1:$BACKSOON_TEST_ADMIN_PORT}
BACKSOON_TEST_NPM_EMAIL=${BACKSOON_TEST_NPM_EMAIL:-admin@backsoon.test}
BACKSOON_TEST_NPM_PASSWORD=${BACKSOON_TEST_NPM_PASSWORD:-backsoon-test-only-pw}
BACKSOON_TEST_WAIT=${BACKSOON_TEST_WAIT:-120}
export BACKSOON_TEST_NPM_URL BACKSOON_TEST_NPM_EMAIL BACKSOON_TEST_NPM_PASSWORD BACKSOON_TEST_WAIT

[ "$#" -gt 0 ] || set -- app1.test=app1:80 app2.test=app2:80

# -I: isolated mode (no cwd / user site imports). Credentials travel via the
# environment, not argv, so they do not show up in `ps`.
exec python3 -I - "$@" <<'PY'
import json, os, sys, time, urllib.error, urllib.parse, urllib.request

base = os.environ["BACKSOON_TEST_NPM_URL"].rstrip("/")
email = os.environ["BACKSOON_TEST_NPM_EMAIL"]
secret = os.environ["BACKSOON_TEST_NPM_PASSWORD"]
deadline = time.monotonic() + int(os.environ["BACKSOON_TEST_WAIT"])

if urllib.parse.urlsplit(base).hostname not in ("127.0.0.1", "localhost", "::1"):
    sys.exit("npm-bootstrap: refusing non-loopback NPM URL (test credentials only)")

# No proxies: the API lives on loopback.
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(method, path, body=None, token=None):
    req = urllib.request.Request(base + path, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    data = None if body is None else json.dumps(body).encode()
    try:
        with opener.open(req, data, timeout=15) as resp:
            raw, status = resp.read(), resp.status
    except urllib.error.HTTPError as err:
        raw, status = err.read(), err.code
    try:
        return status, json.loads(raw) if raw else None
    except ValueError:
        return status, None


def fail(step, status, payload):
    msg = ""
    if isinstance(payload, dict) and isinstance(payload.get("error"), dict):
        msg = payload["error"].get("message", "")
    sys.exit("npm-bootstrap: %s failed (HTTP %s) %s" % (step, status, msg))


# 1. Bounded readiness wait.
status = None
while True:
    try:
        status, info = call("GET", "/api/")
        if status == 200 and info and info.get("status") == "OK":
            break
    except (urllib.error.URLError, OSError):
        pass
    if time.monotonic() > deadline:
        sys.exit("npm-bootstrap: NPM API not ready at %s (last status %s)" % (base, status))
    time.sleep(2)

# 2. First-user setup (only allowed by NPM while no user exists).
if not info.get("setup"):
    status, out = call("POST", "/api/users", {
        "name": "Backsoon Test", "nickname": "test", "email": email,
        "roles": ["admin"], "is_disabled": False,
        "auth": {"type": "password", "secret": secret},
    })
    if status != 201:
        fail("first-user setup", status, out)
    print("user: created %s" % email)
else:
    print("user: setup already done")

# 3. Token.
status, out = call("POST", "/api/tokens", {"identity": email, "secret": secret})
if status != 200 or not out or "token" not in out:
    fail("login (stale volume with other credentials? run harness down)", status, out)
token = out["token"]

# 4. Proxy hosts, converged to the desired state.
status, existing = call("GET", "/api/nginx/proxy-hosts", token=token)
if status != 200:
    fail("list proxy hosts", status, existing)

for spec in sys.argv[1:]:
    try:
        domain, target = spec.split("=", 1)
        fhost, fport = target.rsplit(":", 1)
        want = {
            "domain_names": [domain], "forward_scheme": "http",
            "forward_host": fhost, "forward_port": int(fport),
            "advanced_config": "", "enabled": True,
            "ssl_forced": False, "caching_enabled": False,
            "block_exploits": False, "allow_websocket_upgrade": False,
        }
    except ValueError:
        sys.exit("npm-bootstrap: bad spec %r (want domain=host:port)" % spec)

    host = next((h for h in existing if domain in h["domain_names"]), None)
    if host is None:
        status, out = call("POST", "/api/nginx/proxy-hosts", want, token)
        if status != 201:
            fail("create " + domain, status, out)
        print("proxy host: %s -> %s (id %s, created)" % (domain, target, out["id"]))
        continue

    # NPM returns advanced_config as null when empty; ints for booleans.
    drift = [k for k, v in want.items() if ("" if host.get(k) is None else host[k]) != v]
    if drift:
        status, out = call("PUT", "/api/nginx/proxy-hosts/%s" % host["id"], want, token)
        if status != 200:
            fail("update " + domain, status, out)
        print("proxy host: %s -> %s (id %s, updated: %s)" % (domain, target, host["id"], ", ".join(drift)))
    else:
        print("proxy host: %s -> %s (id %s, unchanged)" % (domain, target, host["id"]))
PY
