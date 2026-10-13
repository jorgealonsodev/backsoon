#!/bin/sh
# Real NPM walking-skeleton checks (U3); never deploys the production stack.
# Usage: sh test/e2e.sh [--help]
# Requires a fresh backsoon-* project. Creates it and removes only its own
# containers/networks/volumes on exit. See docs/npm-harness.md for port overrides.
set -u
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT" || exit 2
case ${1:-} in
  -h|--help) printf '%s\n' 'Usage: sh test/e2e.sh [--help]' 'Runs real NPM fallback/recovery tests in a fresh disposable project.' 'Set BACKSOON_TEST_PROJECT, BACKSOON_TEST_HTTP_PORT and BACKSOON_TEST_ADMIN_PORT as needed.'; exit 0 ;;
  '') ;;
  *) echo 'Usage: sh test/e2e.sh [--help]' >&2; exit 2 ;;
esac
. "$ROOT/test/service-lib.sh"
BACKSOON_TEST_PROJECT=${BACKSOON_TEST_PROJECT:-backsoon-e2e}
BACKSOON_TEST_HTTP_PORT=${BACKSOON_TEST_HTTP_PORT:-8080}
BACKSOON_TEST_ADMIN_PORT=${BACKSOON_TEST_ADMIN_PORT:-8181}
BACKSOON_TEST_WAIT=${BACKSOON_TEST_WAIT:-180}
# Fixture HTML is used only to identify selection. Unit tests cover refresh 5.
BACKSOON_REFRESH=10
BACKSOON_TEST_WITH_BACKSOON=1
export BACKSOON_TEST_PROJECT BACKSOON_TEST_HTTP_PORT BACKSOON_TEST_ADMIN_PORT BACKSOON_TEST_WAIT BACKSOON_REFRESH BACKSOON_TEST_WITH_BACKSOON
OWNED=0
OWN_IMAGE=0
SERVICE_IMAGE="$BACKSOON_TEST_PROJECT-service:local"
dc() { sh "$ROOT/test/harness.sh" compose "$@"; }
die() { printf 'e2e: %s\n' "$*" >&2; exit 2; }
finish() {
  result=$?
  trap - EXIT
  if [ "$OWNED" = 1 ]; then sh "$ROOT/test/harness.sh" down || result=1; fi
  if [ "$OWN_IMAGE" = 1 ] && docker image inspect "$SERVICE_IMAGE" >/dev/null 2>&1; then
    docker image rm "$SERVICE_IMAGE" >/dev/null || result=1
  fi
  exit "$result"
}
trap finish EXIT
trap 'exit 130' INT TERM
# Validate the harness's existing project guard before any Docker mutation.
dc config --quiet || die 'invalid test compose/project settings'
[ -z "$(dc ps -aq)" ] || die 'test project already has containers; use a fresh project or explicitly run e2e-down first'
[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$BACKSOON_TEST_PROJECT")" ] || die 'test project already has volumes; use a fresh project or explicitly run e2e-down first'
mkdir -p "$ROOT/.odd-checks"
ARTIFACTS=$(mktemp -d "$ROOT/.odd-checks/e2e.XXXXXX") || die 'cannot create project-contained test artifacts'
if ! docker image inspect "$SERVICE_IMAGE" >/dev/null 2>&1; then OWN_IMAGE=1; fi
OWNED=1
sh "$ROOT/test/harness.sh" up || die 'harness startup failed'
HTTP_PORT=$BACKSOON_TEST_HTTP_PORT
probe() { host=$1; path=$2; shift 2; req "$HTTP_PORT" "$path" -H "Host: $host" "$@"; }
worker_ids() {
  dc exec -T npm sh -c '
    for cmdline in /proc/[0-9]*/cmdline; do
      [ -r "$cmdline" ] || continue
      title=$(tr "\000" " " < "$cmdline" 2>/dev/null) || continue
      case $title in
        "nginx: worker process"*) pid=${cmdline#/proc/}; printf "%s " "${pid%/cmdline}" ;;
      esac
    done
  '
}
reload() {
  old_workers=$(worker_ids) || die 'cannot snapshot NPM workers'
  [ -n "$old_workers" ] || die 'NPM has no workers to reload'
  dc exec -T npm nginx -s reload || die 'NPM reload failed'
  # Only numeric PIDs from this disposable container are split here.
  # shellcheck disable=SC2086
  set -- $old_workers
  dc exec -T npm sh -c '
    seconds=$1; shift
    deadline=$(( $(date +%s) + seconds ))
    while :; do
      pending=0
      for pid do [ ! -e "/proc/$pid" ] || pending=1; done
      [ "$pending" = 1 ] || exit 0
      [ "$(date +%s)" -lt "$deadline" ] || exit 1
      sleep 0.1
    done
  ' sh "$BACKSOON_TEST_WAIT" "$@" || die 'NPM old worker generation did not retire'
}
install() {
  dc cp "$1" npm:/data/nginx/custom/server_proxy.conf || die 'cannot install test-only global snippet'
  dc exec -T npm nginx -t || die 'NPM rejects global snippet'
  reload
}
pages() { dc run --rm --no-deps -T fixtures "$1" >/dev/null || die 'test page mutation failed'; }
log_count() { dc logs --no-log-prefix backsoon 2>&1 | grep -cE ' [0-9]{3} /' || true; }
wait_probe() {
  wanted=$1; host=$2; marker=$3; seconds=$4
  deadline=$(( $(date +%s) + seconds ))
  while :; do
    probe "$host" /
    if [ "$STATUS" = "$wanted" ]; then
      case $BODY in *"$marker"*) return 0 ;; esac
    fi
    [ "$(date +%s)" -lt "$deadline" ] || die "timed out waiting for $host status $wanted ($marker)"
    sleep 0.2
  done
}

# healthy_requests COUNT [RECORD_FILE]: COUNT identical healthy requests; every one must be 200
# with the unchanged body. Time-to-first-byte is appended to RECORD_FILE, or discarded if omitted.
healthy_requests() {
  for n in $(seq 1 "$1"); do
    timing=$(curl -sS --max-time 10 -H 'Host: errors.test' -o "$ARTIFACTS/perf-body" \
      -w '%{http_code} %{time_starttransfer}' "http://127.0.0.1:$HTTP_PORT/ok") || die 'healthy latency probe failed'
    case $timing in 200\ *) ;; *) die "healthy latency probe got $timing" ;; esac
    [ "$(cat "$ARTIFACTS/perf-body")" = fixture-app-ok ] || die 'healthy response body changed'
    [ -z "${2:-}" ] || printf '%s\n' "${timing#200 }" >> "$2"
  done
}

section 'CA-4: original healthy whoami contract before global fallback'
probe app1.test /before
expect_eq 'app1 initially returns 200' 200 "$STATUS"
expect_has 'app1 initially serves whoami' "$BODY" 'Name: app1'
probe app2.test /before
expect_eq 'app2 initially returns 200' 200 "$STATUS"
expect_has 'app2 initially serves whoami' "$BODY" 'Name: app2'
if [ -f test/fixtures/error-backend.conf ]; then
  dc up -d --wait --wait-timeout "$BACKSOON_TEST_WAIT" app-errors || die 'cannot start error fixture'
  sh "$ROOT/test/npm-bootstrap.sh" errors.test=app-errors:80 || die 'cannot bootstrap error proxy host'
  wait_probe 200 errors.test fixture-app-ok "$BACKSOON_TEST_WAIT"
  probe errors.test /ok
  healthy_baseline=$BODY
fi
# /healthz is a backsoon-private path; a live application owns it, with or without the fallback.
probe app1.test /healthz
expect_eq 'live application /healthz stays 200 before global fallback' 200 "$STATUS"
expect_has 'live application /healthz serves the application before global fallback' "$BODY" 'Name: app1'
if [ -f npm/server_proxy.conf ]; then
  install "$ROOT/npm/server_proxy.conf"
else
  echo 'e2e: global snippet absent; probing the required maintenance behavior (RED)'
fi
probe app1.test /healthz
expect_eq 'live application /healthz stays 200 with global fallback' 200 "$STATUS"
expect_has 'live application /healthz serves the application with global fallback' "$BODY" 'Name: app1'

section 'CA-1: real connection refusal becomes branded maintenance'
dc stop --timeout 5 app1 || die 'cannot stop test app1'
probe app1.test '/arbitrary/nested/path?test=1'
expect_maintenance 'GET through NPM with app1 stopped' 10
expect_has 'NPM returns domain page, not its generic error' "$BODY" 'fixture-host-app1.test'
# The internal fallback request uses a fixed URI; the application URI never selects a backsoon location.
for reserved in /healthz /__backsoon; do
  probe app1.test "$reserved"
  expect_maintenance "stopped application $reserved gets maintenance" 10
  expect_has "stopped application $reserved gets the domain page" "$BODY" 'fixture-host-app1.test'
  expect_eq "stopped application $reserved does not redirect" 0 "$(header_count Location)"
done
# A missing implementation must fail here on public behavior, not missing files.
[ "$FAIL" = 0 ] || { printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"; exit 1; }

section 'CA-9: POST/HEAD/JSON keep the URL and maintenance status'
probe app1.test /api/items -X POST -d 'a=b'
expect_maintenance 'POST through NPM' 10
expect_has 'POST gets the domain HTML, never 405' "$BODY" 'fixture-host-app1.test'
expect_eq 'no redirect Location' 0 "$(header_count Location)"
probe app1.test /api/items -I
expect_maintenance 'HEAD through NPM' 10
expect_eq 'HEAD body empty' 0 "$SIZE"
probe app1.test /api/items -X POST -d 'a=b' -H 'Accept: application/json'
expect_maintenance 'POST JSON through NPM' 10
expect_eq 'exact JSON body' '{"status":"maintenance","retry_after":10}' "$BODY"
expect_eq 'JSON Content-Type' application/json "$(header_value Content-Type)"

section 'CA-10/CA-3: NPM server:port reaches backsoon, then default'
pages 'printf "<html>fixture-target-app1_80</html>" > /pages/target/app1_80.html'
probe app1.test /
expect_has 'domain wins when target page also exists' "$BODY" 'fixture-host-app1.test'
pages 'rm /pages/host/app1.test.html'
probe app1.test /
expect_maintenance 'target page through NPM' 10
expect_has 'actual NPM $server:$port is app1:80' "$BODY" 'fixture-target-app1_80'
pages 'rm /pages/target/app1_80.html'
probe app1.test /
expect_maintenance 'default page through NPM' 10
expect_has 'no matching destination serves default' "$BODY" 'fixture-default'

section 'upstream 502/503/504, unique maintenance headers, legitimate 503'
probe errors.test /ok
expect_eq 'controlled healthy response is 200' 200 "$STATUS"
expect_eq 'healthy body identical with and without global fallback' "$healthy_baseline" "$BODY"
for code in 502 503 504; do
  probe errors.test "/$code"
  expect_maintenance "upstream $code through NPM" 10
  expect_has "upstream $code body replaced by default page" "$BODY" 'fixture-default'
  expect_lacks "upstream $code private body not leaked" "$BODY" 'fixture-upstream-'
done
probe errors.test /ok
expect_eq 'healthy response has no X-Backsoon' 0 "$(header_count X-Backsoon)"

section 'CA-5/CA-16: absent maintenance backend never prevents NPM operation'
# Faithful precondition: NPM's resolver cache is per worker (valid=10s) and shared by every proxy
# host of that worker. Warming must not query the stopped app1 (its cold lookup and the normal 90 s
# connect timeout would confound it), so a disposable proxy host points at the LIVE app-errors
# fixture on a closed port (immediate connection refusal). Its requests fall back to backsoon, which
# makes each worker resolve and cache backsoon. An explicit reload with the old-generation barrier
# first gives fresh workers with a cold app1 cache; app1 is not queried again until the single
# probe. Then backsoon is stopped and ONE immediate app1 request keeps the original 10 s client
# limit, so the stale cached address is really used. Missing coverage, a failed warm request or an
# exceeded window fails before that probe; the evidence is recorded first.
warm_logs() {
  dc exec -T npm sh -c 'cat /data/logs/proxy-host-*_error.log' > "$ARTIFACTS/warm-error-logs" 2> "$ARTIFACTS/warm-logs.stderr"
  dc exec -T npm sh -c 'cat /data/logs/proxy-host-*_access.log' > "$ARTIFACTS/warm-access-logs" 2>> "$ARTIFACTS/warm-logs.stderr"
}
warm_die() { warm_logs || true; die "$@"; }
dc exec -T npm nginx -T > "$ARTIFACTS/npm-compiled.conf" 2> "$ARTIFACTS/npm-compiled.stderr" || die 'cannot record compiled NPM config'
sh "$ROOT/test/npm-bootstrap.sh" warm-backsoon.test=app-errors:1 || die 'cannot bootstrap warm-up proxy host'
reload
active_workers=$(worker_ids) || die 'cannot record active NPM workers'
[ -n "$active_workers" ] || die 'NPM has no active workers'
printf '%s\n' "$active_workers" > "$ARTIFACTS/warm.workers"
: > "$ARTIFACTS/warm.summary"
warm_start=$(date +%s)
for n in $(seq 1 150); do
  curl -sS --max-time 10 -H 'Host: warm-backsoon.test' -D "$ARTIFACTS/warm.headers" -o "$ARTIFACTS/warm.body" \
    -w '%{http_code}' "http://127.0.0.1:$HTTP_PORT/warm-backsoon/$n" > "$ARTIFACTS/warm.code" 2> "$ARTIFACTS/warm.stderr"
  warm_rc=$?
  warm_code=$(cat "$ARTIFACTS/warm.code")
  printf '%s curl_exit=%s http=%s\n' "$n" "$warm_rc" "$warm_code" >> "$ARTIFACTS/warm.summary"
  [ "$warm_rc" = 0 ] || warm_die "warm-cache request $n: curl exit $warm_rc, http $warm_code ($(cat "$ARTIFACTS/warm.stderr"))"
  [ "$warm_code" = 503 ] || warm_die "warm-cache request $n returned $warm_code, not maintenance 503"
  tr -d '\r' < "$ARTIFACTS/warm.headers" | grep -qix 'X-Backsoon: 1' || warm_die "warm-cache request $n lacks X-Backsoon: 1"
  grep -q fixture-default "$ARTIFACTS/warm.body" || warm_die "warm-cache request $n did not return the maintenance page"
done
warm_logs || die 'cannot record worker warming evidence'
for pid in $active_workers; do
  grep -Eq "$pid#$pid:.*connect\\(\\) failed \\(111:.*request: \"GET /warm-backsoon/.*host: \"warm-backsoon\\.test\"" "$ARTIFACTS/warm-error-logs" || die "worker $pid has no connection refusal for the warm-up host; the missing-backend probe was not run"
done
[ "$(worker_ids)" = "$active_workers" ] || die 'NPM worker set changed while warming; the missing-backend probe was not run'
dc stop --timeout 5 backsoon || die 'cannot stop test backsoon'
[ $(( $(date +%s) - warm_start )) -le 8 ] || die 'warm-cache window exceeded 8 s; the missing-backend probe was not run'
printf 'warm workers: %s\n' "$active_workers" > "$ARTIFACTS/missing.workers"
curl -sS --max-time 10 -H 'Host: app1.test' -D "$ARTIFACTS/missing.headers" -o "$ARTIFACTS/missing.body" \
  -w 'http_code=%{http_code} total=%{time_total} connect=%{time_connect} starttransfer=%{time_starttransfer} size=%{size_download}\n' \
  "http://127.0.0.1:$HTTP_PORT/" > "$ARTIFACTS/missing.timing" 2> "$ARTIFACTS/missing.stderr"
missing_rc=$?
printf '%s\n' "$missing_rc" > "$ARTIFACTS/missing.exit"
missing_code=$(sed -n 's/^http_code=\([0-9]*\) .*/\1/p' "$ARTIFACTS/missing.timing")
expect_eq 'first request with warm-cached missing backsoon completes inside the 10 s client limit' 0 "$missing_rc"
expect_eq 'NPM uses its native generic 502 when both backends are stopped' 502 "$missing_code"
expect_eq 'generic NPM error has no maintenance headers' 0 "$(grep -ciE '^(x-backsoon|retry-after):|^cache-control:.*no-store' "$ARTIFACTS/missing.headers" || true)"
missing_body=$(cat "$ARTIFACTS/missing.body")
expect_has 'generic NPM error body is the native 502 page' "$missing_body" '502 Bad Gateway'
expect_lacks 'generic NPM error is not our page' "$missing_body" 'fixture-default'
probe app2.test /
expect_eq 'other application stays available' 200 "$STATUS"
expect_has 'other app still serves whoami' "$BODY" 'Name: app2'
dc exec -T npm nginx -t || die 'NPM config invalid without backsoon'
reload
dc restart npm || die 'NPM restart failed without backsoon'
wait_probe 200 app2.test 'Name: app2' "$BACKSOON_TEST_WAIT"
probe app1.test /
expect_eq 'NPM restarts with absent backsoon and still returns generic 502' 502 "$STATUS"
expect_eq 'restarted generic 502 has no X-Backsoon' 0 "$(header_count X-Backsoon)"
dc start backsoon || die 'cannot restore backsoon'
wait_probe 503 app1.test fixture-default "$BACKSOON_TEST_WAIT"
expect_maintenance 'maintenance backend recovers without NPM reload' 10

section 'automatic application recovery without config intervention'
dc start app1 || die 'cannot restore app1'
wait_probe 200 app1.test 'Name: app1' 12
expect_eq 'restored app has no maintenance headers' 0 "$(header_count X-Backsoon)"
expect_lacks 'restored app is no longer maintenance HTML' "$BODY" 'fixture-default'

section 'RF-10: omit legitimate 503 from the single error_page line'
sed 's/error_page 502 503 504 /error_page 502 504 /' npm/server_proxy.conf > "$ARTIFACTS/server_proxy-502-504.conf"
install "$ARTIFACTS/server_proxy-502-504.conf"
probe errors.test /503
expect_eq 'legitimate application 503 preserved' 503 "$STATUS"
expect_eq 'legitimate application body preserved' fixture-upstream-503 "$BODY"
expect_eq 'legitimate upstream Retry-After preserved, not decorated' 321 "$(header_value Retry-After)"
probe errors.test /502
expect_maintenance '502 still intercepted in variant' 10
install "$ROOT/npm/server_proxy.conf"

section 'RNF-10: disabling only the global file restores original errors'
dc exec -T npm mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.disabled || die 'cannot disable test snippet'
dc exec -T npm nginx -t || die 'NPM invalid after disabling global fallback'
reload
probe errors.test /503
expect_eq 'disabled fallback preserves legitimate application body' fixture-upstream-503 "$BODY"
install "$ROOT/npm/server_proxy.conf"

section 'CA-8/RNF-9: actual nginx syntax and upstream failure trace'
dc exec -T backsoon nginx -t || die 'backsoon config invalid'
dc exec -T npm nginx -t || die 'NPM config invalid'
conf=$(dc exec -T backsoon nginx -T 2>/dev/null | tr -s ' ')
expect_has 'harness runs actual production config worker1' "$conf" 'worker_processes 1;'
expect_has 'harness preserves nginx capture variables' "$conf" '${bs_svc}_${bs_prt}'
expect_eq 'global snippet adds no headers' 0 "$(grep -cE '^[[:space:]]*add_header' npm/server_proxy.conf)"
trace=$(dc exec -T npm sh -c 'cat /data/logs/proxy-host-*_access.log' 2>/dev/null)
# Observed for a controlled HTTP 502: upstream_status "502 : 503" (internal redirect separator) and
# client 503. A DNS-resolution failure instead logs "-, 503", so this field is not universal.
expect_has 'NPM logs controlled upstream 502 then maintenance 503' "$trace" ' 502 : 503 503 - GET http errors.test "/502"'

[ "$FAIL" = 0 ] || { printf '\n%s passed, %s failed\nRNF-5 skipped: functional failures; the run is failed, not partial.\n' "$PASS" "$FAIL"; exit 1; }

# RNF-5, one predeclared method, run once: eight blocks A/B/A/B/A/B/A/B where A has the global
# file disabled and B the canonical one. Each switch waits for the old worker generation to
# retire. Per block: 20 discarded healthy requests, then 50 recorded (200 records per mode).
# Gate: median(B) <= median(A) * 1.05 over all 200 records per mode. All data is kept.
set_mode() {
  if [ "$1" = A ]; then
    dc exec -T npm mv -f /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.disabled || die 'cannot disable global snippet'
    dc exec -T npm nginx -t || die 'NPM invalid with global snippet disabled'
    reload
    dc exec -T npm test ! -e /data/nginx/custom/server_proxy.conf || die 'mode A still has a global snippet'
  else
    install "$ROOT/npm/server_proxy.conf"
    dc exec -T npm test -f /data/nginx/custom/server_proxy.conf || die 'mode B has no global snippet'
  fi
}
section 'RNF-5: healthy latency, 8 alternating blocks (A snippet disabled, B canonical)'
: > "$ARTIFACTS/perf-A.times"
: > "$ARTIFACTS/perf-B.times"
block=0
for mode in A B A B A B A B; do
  block=$((block + 1))
  set_mode "$mode"
  count_before=$(log_count)
  healthy_requests 20
  : > "$ARTIFACTS/block-$block-$mode.times"
  healthy_requests 50 "$ARTIFACTS/block-$block-$mode.times"
  cat "$ARTIFACTS/block-$block-$mode.times" >> "$ARTIFACTS/perf-$mode.times"
  expect_eq "block $block ($mode): 70 healthy requests never contact backsoon" "$count_before" "$(log_count)"
done
perf=$(python3 -I - "$ARTIFACTS" <<'PY'
import statistics, sys
root = sys.argv[1]
def load(name):
    return [float(x) for x in open('%s/%s.times' % (root, name))]
for i, mode in enumerate('ABABABAB', 1):
    print('block %d %s median=%.6fs' % (i, mode, statistics.median(load('block-%d-%s' % (i, mode)))))
before, after = load('perf-A'), load('perf-B')
if len(before) != 200 or len(after) != 200:
    sys.exit('expected 200 records per mode, got %d/%d' % (len(before), len(after)))
mb, ma = statistics.median(before), statistics.median(after)
print('aggregate A_median=%.6fs B_median=%.6fs delta=%.2f%%' % (mb, ma, (ma / mb - 1) * 100))
print('pass' if ma <= mb * 1.05 else 'fail')
PY
) || die 'cannot calculate latency medians'
printf 'RNF-5:\n%s\n' "$perf" | tee "$ARTIFACTS/perf-result.txt"
expect_eq 'aggregate median latency overhead <=5% (200 records per mode)' pass "$(printf '%s\n' "$perf" | tail -n 1)"
final_state=$(dc exec -T npm cat /data/nginx/custom/server_proxy.conf) || die 'cannot read final global snippet'
expect_eq 'run ends with the canonical global snippet enabled' "$(cat npm/server_proxy.conf)" "$final_state"
printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
