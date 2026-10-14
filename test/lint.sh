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

# proxy_network [VALUE]: the external network name docker-compose.yml resolves, with
# BACKSOON_PROXY_NETWORK unset or set to VALUE.
proxy_network() {
  if [ $# -eq 0 ]; then
    env -u BACKSOON_PROXY_NETWORK docker compose -f docker-compose.yml config --format json
  else
    BACKSOON_PROXY_NETWORK=$1 docker compose -f docker-compose.yml config --format json
  fi 2>/dev/null | python3 -I -c 'import json, sys; n = json.load(sys.stdin)["networks"]["proxy"]; print(n["name"] if n.get("external") else "not-external")' 2>/dev/null
}
# npm_image [VALUE]: the NPM image of the test stack, with BACKSOON_TEST_NPM_IMAGE unset or VALUE.
npm_image() {
  if [ $# -eq 0 ]; then
    env -u BACKSOON_TEST_NPM_IMAGE docker compose --profile '*' -f compose.test.yml config --format json
  else
    BACKSOON_TEST_NPM_IMAGE=$1 docker compose --profile '*' -f compose.test.yml config --format json
  fi 2>/dev/null | python3 -I -c 'import json, sys; s = json.load(sys.stdin)["services"]; print(s["npm"]["image"], s["fixtures"]["image"])' 2>/dev/null
}
if command -v python3 >/dev/null 2>&1; then
  got=$(proxy_network)
  if [ "$got" = proxy ]; then pass "the external network defaults to proxy"; else fail "the external network defaults to proxy" "got [$got]"; fi
  got=$(proxy_network npm-nw)
  if [ "$got" = npm-nw ]; then pass "BACKSOON_PROXY_NETWORK names the external network"; else fail "BACKSOON_PROXY_NETWORK names the external network" "got [$got]"; fi
  got=$(npm_image)
  if [ "$got" = 'jc21/nginx-proxy-manager:2.16.0 jc21/nginx-proxy-manager:2.16.0' ]; then pass "the test NPM defaults to 2.16.0"; else fail "the test NPM defaults to 2.16.0" "got [$got]"; fi
  got=$(npm_image jc21/nginx-proxy-manager:2.15.1)
  if [ "$got" = 'jc21/nginx-proxy-manager:2.15.1 jc21/nginx-proxy-manager:2.15.1' ]; then pass "BACKSOON_TEST_NPM_IMAGE selects the test NPM"; else fail "BACKSOON_TEST_NPM_IMAGE selects the test NPM" "got [$got]"; fi
  # compose_json: docker-compose.yml resolved with only the given BACKSOON_ variables set.
  compose_json() {
    env -u BACKSOON_ADMIN_HOST_BIND -u BACKSOON_ADMIN_HOST_PORT -u BACKSOON_NPM_CUSTOM_DIR -u BACKSOON_NPM_INTERCEPT_503 -u BACKSOON_NPM_RESTORE \
      "$@" docker compose -f docker-compose.yml config --format json 2>/dev/null
  }
  admin_ports() {
    compose_json "$@" | python3 -I -c 'import json, sys; p = json.load(sys.stdin)["services"]["backsoon-admin"].get("ports", []); print(" ".join("%s:%s:%s" % (x.get("host_ip", ""), x.get("published"), x.get("target")) for x in p))' 2>/dev/null
  }
  snippet_mount() {
    compose_json "$@" | python3 -I -c 'import json, sys; v = json.load(sys.stdin)["services"]["backsoon-npm-snippet"]["volumes"]; print(" ".join("%s:%s:%s" % (x["type"], x["source"], x["target"]) for x in v))' 2>/dev/null
  }
  got=$(admin_ports)
  if [ "$got" = '127.0.0.1:8181:8080' ]; then pass "the panel is published on 127.0.0.1:8181 by default"; else fail "the panel is published on 127.0.0.1:8181 by default" "got [$got]"; fi
  got=$(admin_ports BACKSOON_ADMIN_HOST_BIND=0.0.0.0 BACKSOON_ADMIN_HOST_PORT=28181)
  if [ "$got" = '0.0.0.0:28181:8080' ]; then pass "BACKSOON_ADMIN_HOST_BIND and _PORT choose the published panel port"; else fail "BACKSOON_ADMIN_HOST_BIND and _PORT choose the published panel port" "got [$got]"; fi
  got=$(snippet_mount)
  if [ "$got" = 'volume:backsoon_npm_custom:/npm-custom' ]; then pass "without BACKSOON_NPM_CUSTOM_DIR the snippet service gets a private volume"; else fail "without BACKSOON_NPM_CUSTOM_DIR the snippet service gets a private volume" "got [$got]"; fi
  got=$(snippet_mount BACKSOON_NPM_CUSTOM_DIR=/docker/nginx-proxy-manager/data/nginx/custom)
  if [ "$got" = 'bind:/docker/nginx-proxy-manager/data/nginx/custom:/npm-custom' ]; then pass "BACKSOON_NPM_CUSTOM_DIR binds NPM's custom directory"; else fail "BACKSOON_NPM_CUSTOM_DIR binds NPM's custom directory" "got [$got]"; fi
  snippet_restore() {
    compose_json "$@" | python3 -I -c 'import json, sys; print(json.load(sys.stdin)["services"]["backsoon-npm-snippet"]["environment"].get("BACKSOON_NPM_RESTORE"))' 2>/dev/null
  }
  got=$(snippet_restore)
  if [ "$got" = false ]; then pass "BACKSOON_NPM_RESTORE defaults to false in the snippet service"; else fail "BACKSOON_NPM_RESTORE defaults to false in the snippet service" "got [$got]"; fi
  got=$(snippet_restore BACKSOON_NPM_RESTORE=true)
  if [ "$got" = true ]; then pass "BACKSOON_NPM_RESTORE reaches the snippet service"; else fail "BACKSOON_NPM_RESTORE reaches the snippet service" "got [$got]"; fi
  got=$(compose_json | python3 -I -c 'import json, sys; s = json.load(sys.stdin)["services"]; n = s["backsoon-npm-snippet"]; deps = [k for k, v in s.items() if "backsoon-npm-snippet" in (v.get("depends_on") or {})]; print(n.get("network_mode"), n.get("restart"), n.get("read_only"), ",".join(n.get("cap_drop", [])), ",".join(sorted(k for k in deps if s[k]["depends_on"]["backsoon-npm-snippet"]["condition"] == "service_completed_successfully")) or "-")' 2>/dev/null)
  if [ "$got" = 'none no True ALL backsoon,backsoon-admin' ]; then pass "the snippet service is one-shot, offline, read-only, without capabilities, and backsoon and the panel wait for it"; else fail "the snippet service is one-shot, offline, read-only, without capabilities, and backsoon and the panel wait for it" "got [$got]"; fi
  got=$(compose_json | python3 -I -c 'import json, sys; s = json.load(sys.stdin)["services"]; print(",".join(sorted(k for k, v in s.items() if v.get("build") and v.get("pull_policy") != "build")) or "-")' 2>/dev/null)
  if [ "$got" = '-' ]; then pass "every built service rebuilds on deploy (pull_policy: build)"; else fail "every built service rebuilds on deploy (pull_policy: build)" "not rebuilt: [$got]"; fi
else
  skip "python3 not available: network and NPM image overrides not checked"
fi

prod=$(docker compose -f docker-compose.yml config 2>/dev/null)
serving=$(printf '%s\n' "$prod" | awk '/^  backsoon:$/ { f = 1; next } /^  [^ ]/ { f = 0 } f')
if printf '%s\n' "$serving" | grep -Eq '^ +(ports|published):'; then fail "backsoon publishes no ports"; else pass "backsoon publishes no ports"; fi
if printf '%s\n' "$serving" | grep -q 'restart: unless-stopped'; then pass "backsoon restarts unless-stopped"; else fail "backsoon restarts unless-stopped"; fi
if printf '%s\n' "$serving" | grep -q 'healthcheck:' && printf '%s\n' "$serving" | grep -q '/healthz'; then pass "backsoon has a /healthz healthcheck"; else fail "backsoon has a /healthz healthcheck"; fi
if printf '%s\n' "$serving" | grep -q 'read_only: true' && printf '%s\n' "$serving" | grep -q 'target: /pages'; then pass "pages volume is read-only for backsoon"; else fail "pages volume is read-only for backsoon"; fi

# --- panel wiring (backsoon-admin) -----------------------------------------------------------
panel=$(printf '%s\n' "$prod" | awk '/^  backsoon-admin:$/ { f = 1; next } /^  [^ ]/ { f = 0 } f')
if [ -n "$panel" ]; then pass "production compose defines backsoon-admin"; else fail "production compose defines backsoon-admin"; fi
if printf '%s\n' "$panel" | grep -q 'restart: unless-stopped'; then pass "backsoon-admin restarts unless-stopped"; else fail "backsoon-admin restarts unless-stopped"; fi
if printf '%s\n' "$panel" | grep -q 'healthcheck:' && printf '%s\n' "$panel" | grep -q -- '-healthcheck'; then pass "backsoon-admin has a -healthcheck healthcheck"; else fail "backsoon-admin has a -healthcheck healthcheck"; fi
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
# A source the images copy but Git ignores would build locally and break a deploy from Git.
if git rev-parse --git-dir >/dev/null 2>&1; then
  ignored=$(awk '$1 == "COPY" { for (i = 2; i < NF; i++) if ($i !~ /^--/ && $i !~ /^\//) print $i }' nginx/Dockerfile admin/Dockerfile |
    while read -r src; do git check-ignore -q "$src" && printf '%s ' "$src"; done)
  if [ -z "$ignored" ]; then pass "no file the Dockerfiles copy is ignored by Git"; else fail "no file the Dockerfiles copy is ignored by Git" "ignored: $ignored"; fi
else
  skip "not a Git checkout: ignored build sources not checked"
fi

for f in test/*.sh nginx/docker-entrypoint.d/*.sh nginx/*.sh; do
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
