# shellcheck shell=sh
# Test cases for test/unit.sh (sourced). Each case drives the service through public curl requests
# or one-shot containers; none inspect nginx internals beyond what RNF-1 asks (process list).
# Fixtures: R5 serves PAGES (BACKSOON_REFRESH=5), DEF serves a seeded volume with the default
# refresh, NODEF serves an empty volume that no initializer ever touches.

HTML_MARK='Volvemos en breve'

case_template() {
  section "templates/maintenance.html (S5, RF-11)"
  t=templates/maintenance.html
  for ph in '{{APP_NAME}}' '{{MESSAGE}}' '{{REFRESH}}' '{{LANG}}' '{{ICON}}'; do
    expect_has "template has placeholder $ph" "$(cat "$t")" "$ph"
  done
  tpl=$(cat "$t")
  expect_has "template refresh meta" "$tpl" '<meta http-equiv="refresh" content="{{REFRESH}}">'
  expect_has "template lang attribute" "$tpl" '<html lang="{{LANG}}">'
  expect_has "template robots noindex" "$tpl" '<meta name="robots" content="noindex">'
  expect_has "template viewport" "$tpl" 'name="viewport"'
  expect_has "template dark mode" "$tpl" 'prefers-color-scheme: dark'
  expect_has "template retry link" "$tpl" '<a href="">Reintentar ahora</a>'
  expect_eq "template has a single h1" 1 "$(grep -c '<h1' "$t")"
  expect_eq "template is self-contained (CA-14 grep)" 0 "$(grep -cE '<script src|<link |@import|url\(http' "$t")"
  if [ "$(wc -c <"$t")" -lt 10240 ]; then ok "template under 10 KB"; else ko "template under 10 KB"; fi
}

case_health_and_basics() {
  section "basic maintenance response, default refresh (RF-1, RF-5, RF-6, RF-4)"
  req "$DEF_PORT" /healthz
  expect_eq "healthz status" 200 "$STATUS"
  expect_eq "healthz body" ok "$BODY"
  expect_eq "healthz carries no X-Backsoon" 0 "$(header_count X-Backsoon)"
  req "$DEF_PORT" /some/path?x=1
  expect_maintenance "GET default" 10
  expect_has "default page body has refresh 10" "$BODY" 'http-equiv="refresh" content="10"'
  expect_has "default page has retry link" "$BODY" 'Reintentar ahora'
  expect_has "default page is HTML" "$(header_value Content-Type)" 'text/html'
  expect_eq "Server header exposes no version" nginx "$(header_value Server)"
  expect_lacks "default page names no application" "$BODY" '{{'
  for leak in 502 503 504 nginx; do expect_lacks "default page does not mention $leak" "$BODY" "$leak"; done
  expect_eq "default page is self-contained" 0 "$(printf '%s\n' "$BODY" | grep -cE '<script src|<link |@import|url\(http')"
  expect_eq "default page has one h1" 1 "$(printf '%s\n' "$BODY" | grep -c '<h1')"
  expect_has "default page says Este servicio" "$BODY" 'Este servicio'
  expect_has "default page is Spanish" "$BODY" '<html lang="es">'

  section "BACKSOON_REFRESH=5 matches body and header (RF-4, CA-11)"
  req "$R5_PORT" /
  expect_maintenance "GET refresh 5" 5
  expect_has "body refresh 5" "$BODY" 'http-equiv="refresh" content="5"'
}

case_selection() {
  section "page selection: host -> target -> default (RF-2, CA-10)"
  req "$R5_PORT" /x -H 'Host: app1.test'
  expect_maintenance "host page" 5
  expect_has "host page body" "$BODY" 'HOST-APP1'
  expect_has "host page content type" "$(header_value Content-Type)" 'text/html'
  req "$R5_PORT" /x -H 'Host: APP1.test:8443'
  expect_has "normalized Host APP1.test:8443 -> app1.test page" "$BODY" 'HOST-APP1'
  req "$R5_PORT" /x -H 'Host: both.test' -H 'X-Backsoon-Target: 10.0.0.5:8081'
  expect_has "host wins over target" "$BODY" 'HOST-BOTH'
  expect_lacks "target page not served when host page exists" "$BODY" 'TARGET-BOTH'
  req "$R5_PORT" /x -H 'Host: nopage.test' -H 'X-Backsoon-Target: 192.168.1.10:8081'
  expect_maintenance "target page" 5
  expect_has "target page body" "$BODY" 'TARGET-ONE'
  req "$R5_PORT" /x -H 'Host: nopage.test' -H 'X-Backsoon-Target: 172.16.0.1:9'
  expect_has "unmatched target falls back to default" "$BODY" 'content="5"'
  expect_lacks "unmatched target serves no custom page" "$BODY" 'TARGET-'
  req "$R5_PORT" /x -H 'Host: nopage.test'
  expect_has "no target header falls back to default" "$BODY" 'content="5"'
}

case_malicious() {
  section "malicious Host and target (RNF-7, CA-10)"
  for h in '../etc/passwd' 'app1.test/../../etc/passwd' '..%2f..%2fetc%2fpasswd' 'a b'; do
    req "$R5_PORT" / -H "Host: $h"
    case $STATUS in
      400) ok "Host [$h] rejected with 400" ;;
      503) expect_has "Host [$h] gets default page" "$BODY" 'content="5"' ;;
      *) ko "Host [$h] must be 400 or default 503" "got $STATUS" ;;
    esac
    expect_lacks "Host [$h] leaks no file content" "$BODY" 'root:'
    expect_lacks "Host [$h] reflects nothing back" "$HEADERS$BODY" 'passwd'
    expect_lacks "Host [$h] never serves a custom page" "$BODY" 'HOST-'
  done
  for t in '../x:1' 'app1:80/../../etc/passwd' '10.0.0.5:8081/..' 'a:123456' '10.0.0.5:' ':80' 'APP1:80' \
    '10.0.0.5:8081,10.0.0.5:8081' '_none' '..%2f..%2fetc%2fpasswd:1'; do
    req "$R5_PORT" / -H 'Host: nopage.test' -H "X-Backsoon-Target: $t"
    expect_eq "target [$t] -> 503" 503 "$STATUS"
    expect_has "target [$t] gets default page" "$BODY" 'content="5"'
    expect_lacks "target [$t] reflects nothing back" "$HEADERS$BODY" 'passwd'
  done
  for u in '/../../etc/passwd' '/%2e%2e/%2e%2e/etc/passwd' '//etc/passwd' '/host/../../etc/passwd'; do
    req "$R5_PORT" "$u"
    case $STATUS in
      503 | 400) ok "URI [$u] -> $STATUS" ;;
      *) ko "URI [$u] must be 503 or 400" "got $STATUS" ;;
    esac
    expect_lacks "URI [$u] leaks no file content" "$BODY" 'root:'
  done
  for u in /__backsoon /__backsoon.json /__backsoon.empty; do
    req "$R5_PORT" "$u"
    expect_maintenance "internal URI $u hit from outside" 5
  done
  req "$R5_PORT" /host/app1.test.html
  expect_eq "page files are not served by path" 503 "$STATUS"
  req "$R5_PORT" / -H 'Range: bytes=0-4' -H 'If-Modified-Since: Fri, 01 Jan 2100 00:00:00 GMT'
  expect_maintenance "conditional and range headers are ignored" 5
  expect_eq "no Content-Range on 503" 0 "$(header_count Content-Range)"
}

case_methods() {
  section "every HTTP method (RF-1, CA-9)"
  for m in GET POST PUT DELETE PATCH OPTIONS; do
    req "$R5_PORT" /api/items -X "$m" -d 'a=b'
    expect_maintenance "$m" 5
    expect_has "$m gets the HTML page" "$BODY" "$HTML_MARK"
  done
  req "$R5_PORT" /api/items -I
  expect_maintenance "HEAD" 5
  expect_eq "HEAD has no body" 0 "$SIZE"
  REQ_BODY_BYTES=2097152 req "$R5_PORT" /upload -X POST
  expect_maintenance "2 MiB POST body" 5
  expect_has "2 MiB POST gets the HTML page" "$BODY" "$HTML_MARK"
}

case_json() {
  section "JSON negotiation (RF-12, CA-9)"
  req "$R5_PORT" / -H 'Accept: application/json'
  expect_maintenance "Accept: application/json" 5
  expect_eq "JSON content type" application/json "$(header_value Content-Type)"
  expect_eq "JSON body" '{"status":"maintenance","retry_after":5}' "$BODY"
  req "$R5_PORT" / -X POST -d 'a=b' -H 'Accept: application/json'
  expect_maintenance "POST with Accept: application/json" 5
  expect_eq "POST JSON body" '{"status":"maintenance","retry_after":5}' "$BODY"
  req "$R5_PORT" / -H 'Accept: application/json, text/plain;q=0.5'
  expect_eq "JSON prefix with extra types" '{"status":"maintenance","retry_after":5}' "$BODY"
  req "$R5_PORT" / -H 'Accept: text/html,application/json'
  expect_has "JSON not first -> HTML" "$BODY" "$HTML_MARK"
  req "$R5_PORT" / -H 'Accept: */*'
  expect_has "Accept */* -> HTML" "$BODY" "$HTML_MARK"
  expect_has "Accept */* content type" "$(header_value Content-Type)" 'text/html'
  req "$R5_PORT" / -H 'Accept:'
  expect_has "no Accept -> HTML" "$BODY" "$HTML_MARK"
  req "$DEF_PORT" / -H 'Accept: application/json'
  expect_eq "JSON body follows default refresh" '{"status":"maintenance","retry_after":10}' "$BODY"
}

case_missing_default() {
  section "missing default.html: 503 with an empty body, never 404 (RF-3)"
  req "$NODEF_PORT" /anything -H 'Host: app1.test' -H 'X-Backsoon-Target: 10.0.0.5:8081'
  expect_maintenance "GET without default" 10
  expect_eq "GET body is empty" 0 "$SIZE"
  req "$NODEF_PORT" /anything -X POST -d 'a=b'
  expect_maintenance "POST without default" 10
  expect_eq "POST body is empty" 0 "$SIZE"
  req "$NODEF_PORT" /anything -I
  expect_maintenance "HEAD without default" 10
  req "$NODEF_PORT" /anything -H 'Accept: application/json'
  expect_maintenance "JSON without default" 10
  expect_eq "JSON still served without default" '{"status":"maintenance","retry_after":10}' "$BODY"
  expect_eq "initializer did not restore the default" "" "$(vol_sh "$VOL_EMPTY" 'ls -A /pages')"
}

case_observability() {
  section "access log: host target status file, no client IP (RNF-9)"
  req "$LOG_PORT" / -H 'Host: app1.test' -H 'X-Backsoon-Target: 192.168.1.10:8081'
  req "$LOG_PORT" / -H 'Host: nopage.test' -H 'X-Backsoon-Target: 192.168.1.10:8081'
  req "$LOG_PORT" / -H 'Host: zzz.test'
  req "$LOG_PORT" /healthz
  gateway=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}' "$LOG_NAME")
  logs=$(docker logs "$LOG_NAME" 2>&1)
  lines=$(printf '%s\n' "$logs" | grep -v -e 'docker-entrypoint' -e '\.sh: ' -e '\[notice\]' -e '^$')
  want='app1.test 192.168.1.10_8081 503 /host/app1.test.html
nopage.test 192.168.1.10_8081 503 /target/192.168.1.10_8081.html
zzz.test _none 503 /default.html'
  expect_eq "access log lines (healthz is not logged)" "$want" "$lines"
  [ -n "$gateway" ] && expect_lacks "logs never contain the client address" "$logs" "$gateway"
  for n in "$R5_NAME" "$DEF_NAME" "$NODEF_NAME"; do
    [ -n "$gateway" ] && expect_lacks "$n logs never contain the client address" "$(docker logs "$n" 2>&1)" "$gateway"
  done
}

case_footprint() {
  section "footprint and hardening (RNF-1, RNF-7)"
  procs=$(docker exec "$R5_NAME" ps -o args | tail -n +2)
  other=$(printf '%s\n' "$procs" | grep -v -e '^nginx: ' -e '^ps -o args$' || true)
  expect_eq "only nginx runs in the container" "" "$other"
  expect_eq "exactly one worker process" 1 "$(printf '%s\n' "$procs" | grep -c '^nginx: worker process')"
  conf=$(docker exec "$R5_NAME" nginx -T 2>/dev/null | tr -s ' ')
  expect_has "worker_processes 1" "$conf" 'worker_processes 1;'
  expect_has "server_tokens off" "$conf" 'server_tokens off;'
  expect_lacks "autoindex stays off" "$conf" 'autoindex on'
  if docker exec "$R5_NAME" sh -c ': > /pages/should-not-exist' 2>/dev/null; then
    ko "pages volume is read-only for serving"
  else
    ok "pages volume is read-only for serving"
  fi
}

case_refresh_validation() {
  section "BACKSOON_REFRESH validation fails fast"
  for v in abc 0 -5 007 100000 '10;}' ''; do
    JOBS=$((JOBS + 1))
    n="$ID-job-$JOBS"
    CONTAINERS="$CONTAINERS $n"
    timeout 30 docker run --rm --name "$n" -e "BACKSOON_REFRESH=$v" "$IMAGE" >/dev/null 2>&1
    rc=$?
    if [ "$rc" -ne 0 ] && [ "$rc" -ne 124 ]; then ok "BACKSOON_REFRESH [$v] refuses to start (exit $rc)"; else ko "BACKSOON_REFRESH [$v] refuses to start" "exit $rc"; fi
  done
}

case_seed() {
  section "seeding the provisional default.html (no overwrite)"
  make_volume "$ID-seed"
  seed_volume "$ID-seed" -e BACKSOON_REFRESH=7 >/dev/null
  expect_eq "seed creates default.html, host/ and target/" "default.html
host
target" "$(vol_sh "$ID-seed" 'ls -A /pages')"
  page=$(vol_sh "$ID-seed" 'cat /pages/default.html')
  expect_has "seeded page uses BACKSOON_REFRESH" "$page" 'http-equiv="refresh" content="7"'
  expect_lacks "seeded page has no unrendered placeholder" "$page" '{{'
  sum1=$(vol_sh "$ID-seed" 'sha256sum /pages/default.html')
  seed_volume "$ID-seed" -e BACKSOON_REFRESH=9 >/dev/null
  expect_eq "re-seeding keeps the existing default.html" "$sum1" "$(vol_sh "$ID-seed" 'sha256sum /pages/default.html')"

  make_volume "$ID-keep"
  vol_sh "$ID-keep" 'mkdir -p /pages/host && printf CUSTOM-DEFAULT > /pages/default.html && printf KEEP-ME > /pages/host/x.test.html'
  seed_volume "$ID-keep" -e BACKSOON_REFRESH=9 >/dev/null
  expect_eq "existing custom default.html survives seeding" CUSTOM-DEFAULT "$(vol_sh "$ID-keep" 'cat /pages/default.html')"
  expect_eq "existing host page survives seeding" KEEP-ME "$(vol_sh "$ID-keep" 'cat /pages/host/x.test.html')"

  make_volume "$ID-bad"
  if seed_volume "$ID-bad" -e BACKSOON_REFRESH=abc >/dev/null 2>&1; then ko "seeding rejects an invalid refresh"; else ok "seeding rejects an invalid refresh"; fi
  expect_eq "rejected seeding writes nothing" "" "$(vol_sh "$ID-bad" 'ls -A /pages')"
}

# Gate only the external sed command, not the entrypoint: both real init scripts have
# opened their render output before either can publish. Releasing the second writer
# after the first publication deterministically exposes a shared temporary inode.
case_seed_concurrency() {
  section "concurrent PID-1 seeders never mutate a published page (B1)"
  mkdir -p "$ROOT/.odd-checks"
  SEED_GATE_DIR=$(mktemp -d "$ROOT/.odd-checks/seed-gate.XXXXXX") || return 1
  mkdir "$SEED_GATE_DIR/bin"
  cat >"$SEED_GATE_DIR/bin/sed" <<'GATE'
#!/bin/sh
set -eu
: >"/gate/ready-$SEED_ROLE"
n=0
while [ ! -e "/gate/release-$SEED_ROLE" ]; do
  n=$((n + 1))
  [ "$n" -lt 200 ] || { echo "seed gate timed out" >&2; exit 124; }
  sleep 0.05
done
exec /bin/sed "$@"
GATE
  chmod +x "$SEED_GATE_DIR/bin/sed"
  race_vol="$ID-seed-race"
  make_volume "$race_vol" || return 1
  for role in 1 2; do
    race_name="$ID-seed-race-$role"
    CONTAINERS="$CONTAINERS $race_name"
    refresh=7
    [ "$role" = 1 ] || refresh=9
    docker run -d --name "$race_name" -v "$race_vol:/pages" \
      -v "$SEED_GATE_DIR:/gate" -e "SEED_ROLE=$role" \
      -e "BACKSOON_REFRESH=$refresh" \
      -e 'PATH=/gate/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
      --entrypoint sh "$IMAGE" /docker-entrypoint.d/45-backsoon-seed.sh >/dev/null || return 1
    waits=0
    while [ ! -e "$SEED_GATE_DIR/ready-$role" ]; do
      waits=$((waits + 1))
      if [ "$waits" -ge 100 ]; then
        ko "seeder $role reaches the render gate"
        docker logs "$race_name" 2>&1
        return 1
      fi
      sleep 0.05
    done
  done
  touch "$SEED_GATE_DIR/release-1"
  rc1=$(timeout 20 docker wait "$ID-seed-race-1") || return 1
  expect_eq "first init exits successfully" 0 "$rc1"
  expect_has "first init reports publication" "$(docker logs "$ID-seed-race-1" 2>&1)" 'seeded /pages/default.html'
  race_sum=$(vol_sh "$race_vol" 'sha256sum /pages/default.html')
  start_service "$ID-seed-race-reader" "$race_vol" -e BACKSOON_REFRESH=7 || return 1
  race_port=$PORT
  req "$race_port" /
  expect_maintenance "first published seed" 7
  expect_has "first page has refresh 7" "$BODY" 'content="7"'
  touch "$SEED_GATE_DIR/release-2"
  rc2=$(timeout 20 docker wait "$ID-seed-race-2") || return 1
  expect_eq "second init exits successfully" 0 "$rc2"
  expect_lacks "second init cannot report replacement" "$(docker logs "$ID-seed-race-2" 2>&1)" 'seeded /pages/default.html'
  expect_eq "later render leaves published inode bytes unchanged" "$race_sum" "$(vol_sh "$race_vol" 'sha256sum /pages/default.html')"
  req "$race_port" /
  expect_maintenance "page after concurrent loser finishes" 7
  expect_has "published page still has refresh 7" "$BODY" 'content="7"'
  expect_lacks "later refresh is never published" "$BODY" 'content="9"'
  expect_eq "all seeder temporary files cleaned" 'default.html
host
target' "$(vol_sh "$race_vol" 'ls -A /pages')"
}

run_all_cases() {
  case_template
  case_health_and_basics
  case_selection
  case_malicious
  case_methods
  case_json
  case_missing_default
  case_observability
  case_footprint
  case_refresh_validation
  case_seed
  case_seed_concurrency
}
