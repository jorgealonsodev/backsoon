#!/bin/sh
# Static validation (U0, CA-8): compose files, shell syntax, page self-containment and
# `nginx -t` on the config exactly as the container renders it. For the panel (admin/): gofmt,
# go vet, the image pins, and the compose wiring (RNF-3, RNF-4a, volume hand-over).
#
# Usage: sh test/lint.sh      Exit 0 only if every check passes. Skipped checks are printed.
#
# Deferred until the NPM integration (T3): npm/server_proxy.conf does not exist yet, so its
# `nginx -t` (wrapped in a test server{}) is skipped with a notice and starts running as soon as
# the file is added. It is also validated against the stock nginx of the backsoon image, not
# against NPM's own nginx; that comes with `make e2e`.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 2

ID="bs-lint-$$"
IMAGE="$ID-image:lint"
FAILED=0

cleanup() {
  trap - EXIT
  docker rm -f "$ID-nginx" "$ID-npm" >/dev/null 2>&1
  docker rmi "$IMAGE" >/dev/null 2>&1
}
trap cleanup EXIT
trap 'exit 130' INT TERM

pass() { printf 'ok    %s\n' "$1"; }
fail() {
  FAILED=1
  printf 'FAIL  %s\n' "$1"
  [ -z "${2:-}" ] || printf '%s\n' "$2" | sed 's/^/      /'
}
skip() { printf 'skip  %s\n' "$1"; }

# check LABEL COMMAND...: pass if the command exits 0, otherwise show its output.
check() {
  _label=$1
  shift
  if _out=$("$@" 2>&1); then pass "$_label"; else fail "$_label" "$_out"; fi
}

command -v docker >/dev/null 2>&1 || { echo "lint needs docker" >&2; exit 2; }

# --- compose -------------------------------------------------------------------
check "docker compose config (docker-compose.yml)" docker compose -f docker-compose.yml config -q
if [ -f compose.test.yml ]; then
  check "docker compose config (compose.test.yml, all profiles)" docker compose --profile '*' -f compose.test.yml config -q
else
  skip "compose.test.yml not present"
fi

prod=$(docker compose -f docker-compose.yml config 2>/dev/null)
serving=$(printf '%s\n' "$prod" | awk '/^  backsoon:$/ { f = 1; next } /^  [^ ]/ { f = 0 } f')
if printf '%s\n' "$prod" | grep -Eq '^ +(ports|published):'; then fail "production compose publishes no ports"; else pass "production compose publishes no ports"; fi
if printf '%s\n' "$serving" | grep -q 'restart: unless-stopped'; then pass "backsoon restarts unless-stopped"; else fail "backsoon restarts unless-stopped"; fi
if printf '%s\n' "$serving" | grep -q 'healthcheck:' && printf '%s\n' "$serving" | grep -q '/healthz'; then pass "backsoon has a /healthz healthcheck"; else fail "backsoon has a /healthz healthcheck"; fi
if printf '%s\n' "$serving" | grep -q 'read_only: true' && printf '%s\n' "$serving" | grep -q 'target: /pages'; then pass "pages volume is read-only for backsoon"; else fail "pages volume is read-only for backsoon"; fi

# --- panel wiring (backsoon-admin) -----------------------------------------------------------
panel=$(printf '%s\n' "$prod" | awk '/^  backsoon-admin:$/ { f = 1; next } /^  [^ ]/ { f = 0 } f')
if [ -n "$panel" ]; then pass "production compose defines backsoon-admin"; else fail "production compose defines backsoon-admin"; fi
if printf '%s\n' "$panel" | grep -q 'restart: unless-stopped'; then pass "backsoon-admin restarts unless-stopped"; else fail "backsoon-admin restarts unless-stopped"; fi
if printf '%s\n' "$panel" | grep -q 'healthcheck:' && printf '%s\n' "$panel" | grep -q -- '-healthcheck'; then pass "backsoon-admin has a -healthcheck healthcheck"; else fail "backsoon-admin has a -healthcheck healthcheck"; fi
if printf '%s\n' "$panel" | grep -Eq '^ +(ports|published):'; then fail "backsoon-admin publishes no ports"; else pass "backsoon-admin publishes no ports"; fi
if printf '%s\n' "$panel" | grep -q 'proxy: null' && printf '%s\n' "$panel" | grep -q 'source: backsoon_pages' && printf '%s\n' "$panel" | grep -q 'source: backsoon_meta'; then pass "backsoon-admin joins proxy and mounts the pages and meta volumes"; else fail "backsoon-admin joins proxy and mounts the pages and meta volumes"; fi
if printf '%s\n' "$panel" | grep -q 'condition: service_completed_successfully'; then pass "backsoon-admin waits for backsoon-init"; else fail "backsoon-admin waits for backsoon-init"; fi
if printf '%s\n' "$panel" | grep -q 'BACKSOON_REFRESH: "10"'; then pass "backsoon-admin shares the BACKSOON_REFRESH default"; else fail "backsoon-admin shares the BACKSOON_REFRESH default"; fi
if printf '%s\n' "$serving" | grep -q 'backsoon-admin'; then fail "backsoon does not depend on the panel (RNF-4a)"; else pass "backsoon does not depend on the panel (RNF-4a)"; fi
# The root init hands the pages volume to the panel's uid; the test stack must run the same command.
if command -v python3 >/dev/null 2>&1; then
  if out=$(python3 -I - "$(docker compose -f docker-compose.yml config --format json)" "$(docker compose --profile '*' -f compose.test.yml config --format json)" <<'PY' 2>&1
import json, sys
prod = json.loads(sys.argv[1])["services"]["backsoon-init"]
test = json.loads(sys.argv[2])["services"]["pages-init"]
for key in ("entrypoint", "command"):
    if prod.get(key) != test.get(key):
        sys.exit("%s differs: production %r, test %r" % (key, prod.get(key), test.get(key)))
if "chown -R 65532:65532 /pages" not in " ".join(prod.get("command") or []):
    sys.exit("backsoon-init does not hand /pages to uid 65532")
PY
  ); then pass "backsoon-init hands /pages to 65532 and the test pages-init runs the same command"; else fail "backsoon-init hands /pages to 65532 and the test pages-init runs the same command" "$out"; fi
else
  skip "python3 not found: backsoon-init / pages-init parity check"
fi

# --- image pin and page hygiene --------------------------------------------------
if grep -Eq '^FROM nginx:[0-9]+\.[0-9]+\.[0-9]+-alpine-slim@sha256:[0-9a-f]{64}$' nginx/Dockerfile; then pass "nginx image is pinned (version + digest)"; else fail "nginx image is pinned (version + digest)"; fi
if grep -Eq '^FROM golang:[0-9]+\.[0-9]+\.[0-9]+-alpine@sha256:[0-9a-f]{64} AS build$' admin/Dockerfile; then pass "panel builder image is pinned (version + digest)"; else fail "panel builder image is pinned (version + digest)"; fi
if grep -Eq '^FROM gcr\.io/distroless/static-debian12:nonroot@sha256:[0-9a-f]{64}$' admin/Dockerfile; then pass "panel runtime image is pinned (distroless nonroot + digest)"; else fail "panel runtime image is pinned (distroless nonroot + digest)"; fi
check "docker build --check admin/Dockerfile" docker build --check -f admin/Dockerfile .

# --- panel Go sources --------------------------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  unformatted=$(gofmt -l admin 2>&1)
  if [ -z "$unformatted" ]; then pass "gofmt -l admin is empty"; else fail "gofmt -l admin is empty" "$unformatted"; fi
  if out=$(cd admin && go vet ./... 2>&1); then pass "go vet ./... (admin)"; else fail "go vet ./... (admin)" "$out"; fi
else
  fail "go toolchain not found: gofmt and go vet for admin/ need it"
fi
if grep -rEn '<script src|<link |@import|url\(http' templates/; then fail "templates are self-contained (CA-14)"; else pass "templates are self-contained (CA-14)"; fi
for f in test/*.sh nginx/docker-entrypoint.d/*.sh; do
  check "sh -n $f" sh -n "$f"
done

# --- nginx -t on the rendered configuration --------------------------------------
if build_out=$(docker build -q -t "$IMAGE" -f nginx/Dockerfile . 2>&1); then
  pass "docker build nginx/Dockerfile"
  if out=$(docker run --rm --name "$ID-nginx" "$IMAGE" nginx -t 2>&1); then
    pass "nginx -t (backsoon, rendered config)"
  else
    fail "nginx -t (backsoon, rendered config)" "$out"
  fi

  snippet=${BACKSOON_NPM_SNIPPET:-npm/server_proxy.conf}
  if [ -f "$snippet" ] || [ -n "${BACKSOON_NPM_SNIPPET:-}" ]; then
    # NPM defines $server and $port per proxy host; stand-ins let the snippet parse on its own.
    if out=$(
      {
        printf 'events {}\nhttp {\n  server {\n    listen 8080;\n    server_name lint.test;\n'
        printf '    set $server app1.test;\n    set $port 80;\n'
        cat "$snippet"
        printf '\n  }\n}\n'
      } | docker run --rm -i --name "$ID-npm" --entrypoint sh "$IMAGE" -c 'cat >/tmp/npm-wrapped.conf && nginx -t -c /tmp/npm-wrapped.conf' 2>&1
    ); then
      pass "nginx -t ($snippet wrapped in a test server{})"
    else
      fail "nginx -t ($snippet wrapped in a test server{})" "$out"
    fi
  else
    skip "npm/server_proxy.conf not present: NPM snippet check deferred to T3 (U3)"
  fi
else
  fail "docker build nginx/Dockerfile" "$build_out"
fi

[ "$FAILED" -eq 0 ] && echo "lint: all checks passed" || echo "lint: FAILED"
[ "$FAILED" -eq 0 ]
