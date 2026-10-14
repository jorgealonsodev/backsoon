# shellcheck shell=sh
# Helpers for test/unit.sh (sourced, never executed). Expects ID and IMAGE to be set by the caller.
# Everything created here is tracked in the lists below so cleanup only removes our own resources.

PASS=0
FAIL=0
JOBS=0
CONTAINERS=""
VOLUMES=""
IMAGES=""
SEED_GATE_DIR=""

# --- reporting ---------------------------------------------------------------

section() { printf '\n== %s\n' "$1"; }
ok() { PASS=$((PASS + 1)); printf 'ok    %s\n' "$1"; }
ko() {
  FAIL=$((FAIL + 1))
  printf 'FAIL  %s\n' "$1"
  [ -z "${2:-}" ] || printf '      %s\n' "$2"
}

expect_eq() { if [ "$2" = "$3" ]; then ok "$1"; else ko "$1" "expected [$2], got [$3]"; fi; }
expect_has() { case $2 in *"$3"*) ok "$1" ;; *) ko "$1" "missing [$3]" ;; esac; }
expect_lacks() { case $2 in *"$3"*) ko "$1" "unexpected [$3]" ;; *) ok "$1" ;; esac; }

# --- HTTP --------------------------------------------------------------------

_curl() {
  curl -s -i --max-time 10 --path-as-is -H 'Expect:' \
    -w '\n@@STATUS=%{http_code} SIZE=%{size_download}' "$@"
}

# req PORT PATH [curl args...]  ->  STATUS HEADERS BODY SIZE (body bytes)
# REQ_BODY_BYTES=N sends N bytes of generated data as the request body. (A pipeline would run req
# in a subshell and lose the variables it sets, so the body is generated here instead.)
req() {
  _port=$1
  _path=$2
  shift 2
  if [ -n "${REQ_BODY_BYTES:-}" ]; then
    _raw=$(yes | head -c "$REQ_BODY_BYTES" | _curl "$@" --data-binary @- "http://127.0.0.1:$_port$_path")
  else
    _raw=$(_curl "$@" "http://127.0.0.1:$_port$_path")
  fi
  _tail=$(printf '%s\n' "$_raw" | tail -n 1)
  STATUS=${_tail#@@STATUS=}
  STATUS=${STATUS%% *}
  SIZE=${_tail##*SIZE=}
  HEADERS=$(printf '%s\n' "$_raw" | awk '/^\r?$/ { exit } { print }')
  BODY=$(printf '%s\n' "$_raw" | awk 'f { print } !f && /^\r?$/ { f = 1 }' | sed '$d')
}

header_count() { printf '%s\n' "$HEADERS" | tr -d '\r' | grep -ic "^$1:" || true; }
header_value() {
  printf '%s\n' "$HEADERS" | tr -d '\r' |
    awk -v n="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]'):" \
      'index(tolower($0), n) == 1 { sub(/^[^:]*:[ \t]*/, ""); print; exit }'
}

# expect_header LABEL NAME VALUE: the header appears exactly once with exactly VALUE.
expect_header() {
  _c=$(header_count "$2")
  _v=$(header_value "$2")
  if [ "$_c" = 1 ] && [ "$_v" = "$3" ]; then ok "$1"; else ko "$1" "$2: seen $_c time(s), value [$_v], expected once [$3]"; fi
}

# The visitor-facing policy: no script, no external resource, no framing, no form target.
BACKSOON_CSP="default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; frame-ancestors 'none'; base-uri 'none'"

# expect_maintenance LABEL REFRESH: 503 plus the maintenance and security headers, each exactly once.
expect_maintenance() {
  expect_eq "$1: status 503" 503 "$STATUS"
  expect_header "$1: Retry-After" Retry-After "$2"
  expect_header "$1: Cache-Control" Cache-Control no-store
  expect_header "$1: X-Backsoon" X-Backsoon 1
  expect_header "$1: Content-Security-Policy" Content-Security-Policy "$BACKSOON_CSP"
  expect_header "$1: X-Content-Type-Options" X-Content-Type-Options nosniff
}

# --- Docker ------------------------------------------------------------------

make_volume() {
  VOLUMES="$VOLUMES $1"
  docker volume create "$1" >/dev/null
}

# job DOCKER_RUN_ARGS...: one-shot container with a tracked name that is removed on exit.
job() {
  JOBS=$((JOBS + 1))
  _name="$ID-job-$JOBS"
  CONTAINERS="$CONTAINERS $_name"
  docker run --rm --name "$_name" "$@"
}

# seed_volume VOLUME [docker run args...]: the same command the production init service runs.
seed_volume() {
  _vol=$1
  shift
  job -v "$_vol:/pages" "$@" --entrypoint sh "$IMAGE" /docker-entrypoint.d/45-backsoon-seed.sh
}

# vol_sh VOLUME SCRIPT: run a shell snippet against the volume mounted at /pages (read-write).
vol_sh() { job -v "$1:/pages" --entrypoint sh "$IMAGE" -c "$2"; }

wait_ready() {
  _n=0
  while [ "$_n" -lt 40 ]; do
    [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$1/healthz")" = 200 ] && return 0
    _n=$((_n + 1))
    sleep 0.25
  done
  return 1
}

# start_service NAME VOLUME [docker run args...]: serves VOLUME read-only on a loopback ephemeral
# port; sets PORT. Mirrors production: no network proxy, no fixed host port.
start_service() {
  _name=$1
  _vol=$2
  shift 2
  CONTAINERS="$CONTAINERS $_name"
  docker run -d --name "$_name" -v "$_vol:/pages:ro" -p 127.0.0.1::80 "$@" "$IMAGE" >/dev/null || return 1
  PORT=$(docker port "$_name" 80/tcp | sed -n '1s/.*://p')
  if [ -n "$PORT" ] && wait_ready "$PORT"; then return 0; fi
  docker logs "$_name" 2>&1 | tail -n 20
  return 1
}

cleanup() {
  trap - EXIT
  for _c in $CONTAINERS; do docker rm -f "$_c" >/dev/null 2>&1; done
  for _v in $VOLUMES; do docker volume rm "$_v" >/dev/null 2>&1; done
  for _i in $IMAGES; do docker rmi "$_i" >/dev/null 2>&1; done
  if [ -n "$SEED_GATE_DIR" ]; then
    rm -f "$SEED_GATE_DIR/bin/sed" "$SEED_GATE_DIR/ready-1" "$SEED_GATE_DIR/ready-2" \
      "$SEED_GATE_DIR/release-1" "$SEED_GATE_DIR/release-2"
    rmdir "$SEED_GATE_DIR/bin" "$SEED_GATE_DIR"
  fi
}
