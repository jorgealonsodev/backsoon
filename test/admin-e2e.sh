#!/bin/sh
# backsoon-admin end-to-end checks (U5/U6): real NPM + real backsoon + the real panel image,
# all driven through NPM (Host: admin.test) so X-Real-IP and the real proxy path are exercised.
# Never deploys the production stack.
# Usage: sh test/admin-e2e.sh [--help]
# Requires a fresh backsoon-* project. Creates it and removes only its own
# containers/networks/volumes/images on exit. See docs/npm-harness.md for port overrides.
#
# The assertions rest on contracts, not on the panel's look: routes, HTTP statuses, headers
# (CSP, Location, Retry-After, Set-Cookie flags), form field names, the page names and
# destinations that were submitted, and what backsoon/NPM publish. Headings, button labels,
# CSS classes and explanatory copy are deliberately never asserted.
set -u
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT" || exit 2
case ${1:-} in
  -h|--help) printf '%s\n' 'Usage: sh test/admin-e2e.sh [--help]' 'Runs the panel (backsoon-admin) tests through real NPM and backsoon in a fresh disposable project.' 'Set BACKSOON_TEST_PROJECT (default backsoon-e2e-admin), BACKSOON_TEST_HTTP_PORT and BACKSOON_TEST_ADMIN_PORT as needed.'; exit 0 ;;
  '') ;;
  *) echo 'Usage: sh test/admin-e2e.sh [--help]' >&2; exit 2 ;;
esac
. "$ROOT/test/service-lib.sh"
BACKSOON_TEST_PROJECT=${BACKSOON_TEST_PROJECT:-backsoon-e2e-admin}
BACKSOON_TEST_HTTP_PORT=${BACKSOON_TEST_HTTP_PORT:-8080}
BACKSOON_TEST_ADMIN_PORT=${BACKSOON_TEST_ADMIN_PORT:-8181}
BACKSOON_TEST_WAIT=${BACKSOON_TEST_WAIT:-180}
BACKSOON_REFRESH=10
# Panel mode of the harness: pages-init (the production volume hand-over), backsoon,
# backsoon-admin and the admin.test proxy host; the root-owned page fixtures are not installed.
BACKSOON_TEST_WITH_ADMIN=1
export BACKSOON_TEST_PROJECT BACKSOON_TEST_HTTP_PORT BACKSOON_TEST_ADMIN_PORT BACKSOON_TEST_WAIT BACKSOON_REFRESH BACKSOON_TEST_WITH_ADMIN
OWNED=0
SERVICE_IMAGE="$BACKSOON_TEST_PROJECT-service:local"
ADMIN_IMAGE="$BACKSOON_TEST_PROJECT-admin:local"
OWN_SERVICE_IMAGE=0
OWN_ADMIN_IMAGE=0
NOCREDS_PREFIX="$BACKSOON_TEST_PROJECT-nocreds"
dc() { sh "$ROOT/test/harness.sh" compose "$@"; }
die() { printf 'admin-e2e: %s\n' "$*" >&2; exit 2; }
finish() {
  result=$?
  trap - EXIT
  for n in 1 2 3 4; do docker rm -f "$NOCREDS_PREFIX-$n" >/dev/null 2>&1; done
  if [ "$OWNED" = 1 ]; then sh "$ROOT/test/harness.sh" down || result=1; fi
  for pair in "$OWN_SERVICE_IMAGE:$SERVICE_IMAGE" "$OWN_ADMIN_IMAGE:$ADMIN_IMAGE"; do
    if [ "${pair%%:*}" = 1 ] && docker image inspect "${pair#*:}" >/dev/null 2>&1; then
      docker image rm "${pair#*:}" >/dev/null || result=1
    fi
  done
  exit "$result"
}
trap finish EXIT
trap 'exit 130' INT TERM
# Validate the harness's existing project guard before any Docker mutation.
dc config --quiet || die 'invalid test compose/project settings'
[ -z "$(dc ps -aq)" ] || die 'test project already has containers; use a fresh project or explicitly run e2e-down first'
[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$BACKSOON_TEST_PROJECT")" ] || die 'test project already has volumes; use a fresh project or explicitly run e2e-down first'
mkdir -p "$ROOT/.odd-checks"
ARTIFACTS=$(mktemp -d "$ROOT/.odd-checks/admin-e2e.XXXXXX") || die 'cannot create project-contained test artifacts'
TMPDIR=$ARTIFACTS
export TMPDIR
if ! docker image inspect "$SERVICE_IMAGE" >/dev/null 2>&1; then OWN_SERVICE_IMAGE=1; fi
if ! docker image inspect "$ADMIN_IMAGE" >/dev/null 2>&1; then OWN_ADMIN_IMAGE=1; fi
OWNED=1
sh "$ROOT/test/harness.sh" up || die 'harness startup failed'

HTTP_PORT=$BACKSOON_TEST_HTTP_PORT
FIX="$ROOT/test/fixtures/admin"
PASSWORD=backsoon-test-only-panel-pw
SECRET=backsoon-test-only-session-secret-0123456789abcdef
DEFAULT_MARK='Estamos realizando tareas de mantenimiento'
COOKIE=''
CSRF=''
PAGE_ID=''

# --- helpers ------------------------------------------------------------------------------

# anon PATH [curl args]: unauthenticated request to the panel through NPM.
anon() { _p=$1; shift; req "$HTTP_PORT" "$_p" -H 'Host: admin.test' "$@"; }
# panel PATH [curl args]: the same with the session cookie (Secure: sent by hand over plain http).
panel() { _p=$1; shift; req "$HTTP_PORT" "$_p" -H 'Host: admin.test' -H "Cookie: $COOKIE" "$@"; }
# probe HOST PATH [curl args]: a proxied application request through NPM.
probe() { _h=$1; _p=$2; shift 2; req "$HTTP_PORT" "$_p" -H "Host: $_h" "$@"; }
# served HOST [TARGET]: what backsoon itself answers for HOST/TARGET, asked from the NPM container.
served() {
  _raw=$(dc exec -T npm curl -s -i --max-time 5 -H "Host: $1" -H "X-Backsoon-Target: ${2:-none}" \
    -w '\n@@STATUS=%{http_code}' http://backsoon/)
  _tail=$(printf '%s\n' "$_raw" | tail -n 1)
  STATUS=${_tail#@@STATUS=}
  HEADERS=$(printf '%s\n' "$_raw" | awk '/^\r?$/ { exit } { print }')
  BODY=$(printf '%s\n' "$_raw" | awk 'f { print } !f && /^\r?$/ { f = 1 }' | sed '$d')
}
set_cookie_line() { printf '%s\n' "$HEADERS" | tr -d '\r' | grep -i "^set-cookie: $1=" | head -n 1; }
set_cookie_value() { set_cookie_line "$1" | sed -n "s/^[Ss]et-[Cc]ookie: $1=\\([^;]*\\).*/\\1/p"; }
# field_value NAME: value attribute of the <input name="NAME"> found in the last BODY.
field_value() {
  printf '%s\n' "$BODY" | grep -o "<input[^>]*name=\"$1\"[^>]*>" | head -n 1 | sed -n 's/.*value="\([^"]*\)".*/\1/p'
}
focus_of() { header_value Location | sed -n 's/.*focus=\([^&#]*\).*/\1/p'; }
# page_hrefs: the distinct /pages/<id> links of the last BODY (the pages the directory lists).
page_hrefs() { printf '%s\n' "$BODY" | grep -o 'href="/pages/[a-z0-9_-]*"' | sed 's/^href="//; s/"$//' | grep -v '^/pages/new$' | sort -u; }
# page_count: GET / and print how many distinct pages it links (runs in a subshell).
page_count() { panel / -H 'Accept: text/html'; page_hrefs | wc -l | tr -d ' '; }
list() { panel / -H 'Accept: text/html'; [ "$STATUS" = 200 ] || die "cannot list pages (status $STATUS)"; }
expect_listed() { expect_has "$1" "$BODY" "$2"; }
expect_not_listed() { expect_lacks "$1" "$BODY" "$2"; }
expect_ge() { if [ "$2" -ge "$3" ]; then ok "$1 ($2)"; else ko "$1" "got $2, want >= $3"; fi; }
# expect_line LABEL TEXT LINE: LINE is one whole line of TEXT.
expect_line() { if printf '%s\n' "$2" | grep -Fxq "$3"; then ok "$1"; else ko "$1" "no line [$3]"; fi; }
# edit_page ID: the edit form of a page, which carries the destinations it owns.
edit_page() { panel "/pages/$1" -H 'Accept: text/html'; [ "$STATUS" = 200 ] || die "cannot open page $1 (status $STATUS)"; }

# create_template NAME DESTS APP [extra --data-urlencode args]
create_template() {
  _n=$1; _d=$2; _a=$3; shift 3
  panel /pages -X POST --data-urlencode "csrf=$CSRF" --data-urlencode "name=$_n" \
    --data-urlencode "destinations=$_d" --data-urlencode source=template --data-urlencode "app_name=$_a" \
    --data-urlencode 'message=Back soon.' --data-urlencode lang=en --data-urlencode 'icon=🔧' "$@"
}
# create_upload NAME DESTS FILE [extra -F args]
create_upload() {
  _n=$1; _d=$2; _f=$3; shift 3
  panel /pages -X POST -F "csrf=$CSRF" -F "name=$_n" -F "destinations=$_d" -F source=upload \
    -F "file=@$_f;type=text/html" "$@"
}
# expect_created LABEL: 303 back to the directory, never a publish failure; sets PAGE_ID.
expect_created() {
  expect_eq "$1: 303" 303 "$STATUS"
  case $(header_value Location) in
    *publish-failed*) ko "$1: published" "Location [$(header_value Location)]" ;;
    *) ok "$1: published" ;;
  esac
  PAGE_ID=$(focus_of)
  if [ -n "$PAGE_ID" ]; then ok "$1: new page id in Location"; else ko "$1: new page id in Location" "Location [$(header_value Location)]"; fi
}
# expect_default LABEL HOST [TARGET]: backsoon still answers HOST with the default page.
expect_default() {
  served "$2" "${3:-none}"
  expect_maintenance "$1: backsoon answers $2" 10
  expect_has "$1: $2 shows the default page" "$BODY" "$DEFAULT_MARK"
}

# --- NPM reload barrier and the global fallback snippet (same method as test/e2e.sh) -------------
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
# wait_for LABEL SECONDS COMMAND...: bounded poll, never loops forever.
# stop_service NAME: stop a container, then reload NPM. NPM keeps a per-worker DNS cache (10 s) of
# the stopped name; a warm entry makes the next request wait for NPM's 90 s connect timeout
# (see test/e2e.sh and docs/npm-harness.md). Fresh workers resolve NXDOMAIN at once, which is the
# fast failure that production shows once the cache has expired.
stop_service() {
  dc stop --timeout 5 "$1" || die "cannot stop $1"
  reload
}
wait_for() {
  _label=$1; _secs=$2; shift 2
  _end=$(( $(date +%s) + _secs ))
  until "$@" >/dev/null 2>&1; do
    [ "$(date +%s)" -lt "$_end" ] || die "timed out waiting for $_label"
    sleep 0.5
  done
}
panel_up() { anon /healthz; [ "$STATUS" = 200 ]; }
health_of() { docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q "$1")"; }
panel_healthy() { [ "$(health_of backsoon-admin)" = healthy ]; }
started_at() { docker inspect -f '{{.State.StartedAt}}' "$(dc ps -q "$1")"; }
app_probe() {
  probe "$1" /
  [ "$STATUS" = "$2" ] || return 1
  case $BODY in *"$3"*) return 0 ;; esac
  return 1
}

section 'setup: NPM proxy hosts, global fallback snippet, panel reachable only through NPM'
wait_for 'panel healthy' "$BACKSOON_TEST_WAIT" panel_healthy
wait_for 'panel through NPM' "$BACKSOON_TEST_WAIT" panel_up
install "$ROOT/npm/server_proxy.conf"
panel_up
expect_eq 'panel /healthz through NPM is 200' 200 "$STATUS"
expect_has 'panel /healthz body' "$BODY" ok
expect_eq 'panel container publishes no ports' '{}' "$(docker inspect -f '{{json .NetworkSettings.Ports}}' "$(dc ps -q backsoon-admin)")"
probe app1.test /
expect_eq 'app1 serves before any panel page' 200 "$STATUS"
expect_has 'app1 serves whoami' "$BODY" 'Name: app1'
expect_default 'fresh volume (seeded by pages-init)' unknown.test
SERVICE_STARTED=$(started_at backsoon)
PANEL_STARTED=$(started_at backsoon-admin)

section 'CA-17: nothing works without a session'
for navigation in / /pages/new /pages/default /assets/fonts/archivo.woff2 /nope; do
  anon "$navigation" -H 'Accept: text/html,application/xhtml+xml'
  expect_eq "GET $navigation (navigation) -> 303" 303 "$STATUS"
  expect_eq "GET $navigation (navigation) -> /login" /login "$(header_value Location)"
  expect_eq "GET $navigation (navigation) leaks no form token" 0 "$(printf '%s' "$BODY" | grep -c 'name="csrf"')"
done
# A request aimed at a frame or at the preview path gets a bare 401 notice, never the login form.
for navigation in /pages/default/preview; do
  anon "$navigation" -H 'Accept: text/html,application/xhtml+xml'
  expect_eq "GET $navigation (navigation) -> 401" 401 "$STATUS"
  expect_eq "GET $navigation (navigation) does not redirect" 0 "$(header_count Location)"
  expect_eq "GET $navigation (navigation) leaks no page" 0 "$(printf '%s' "$BODY" | grep -c 'fixture\|Estamos realizando')"
done
for fetched in / /pages/new /pages/default/preview; do
  anon "$fetched" -H 'Accept: application/json' -H 'Sec-Fetch-Mode: cors'
  expect_eq "GET $fetched (fetch) -> 401" 401 "$STATUS"
  expect_eq "GET $fetched (fetch) has no redirect" 0 "$(header_count Location)"
done
anon /pages -X POST -H 'Accept: application/json' -H 'Sec-Fetch-Mode: cors' --data-urlencode 'name=Ghost-e2e' --data-urlencode 'destinations=ghost.test' --data-urlencode source=template --data-urlencode app_name=Ghost
expect_eq 'POST /pages (fetch) -> 401' 401 "$STATUS"
anon /pages -X POST -H 'Accept: text/html' --data-urlencode 'name=Ghost-e2e' --data-urlencode 'destinations=ghost.test' --data-urlencode source=template --data-urlencode app_name=Ghost
expect_eq 'POST /pages (navigation) -> 303' 303 "$STATUS"
expect_eq 'POST /pages (navigation) -> /login' /login "$(header_value Location)"
anon /pages/default/delete -X POST -H 'Accept: application/json' --data-urlencode confirm=true
expect_eq 'POST /pages/default/delete (fetch) -> 401' 401 "$STATUS"
expect_default 'rejected unauthenticated POST created no page' ghost.test
anon /healthz
expect_eq '/healthz is public' 200 "$STATUS"
anon /login
expect_eq '/login is public' 200 "$STATUS"

section 'CA-17: a panel without credentials does not start'
n=0
# cred_case LABEL VARIABLE docker-run-env-args...: the real image with exactly that environment.
cred_case() {
  label=$1; named=$2; shift 2
  n=$((n + 1))
  out=$(docker run --rm --name "$NOCREDS_PREFIX-$n" --label com.backsoon.test=true --network none "$@" "$ADMIN_IMAGE" 2>&1)
  code=$?
  if [ "$code" != 0 ]; then ok "$label: exits non-zero ($code)"; else ko "$label: exits non-zero" "exit 0"; fi
  expect_has "$label: message names $named" "$out" "$named"
  expect_lacks "$label: message does not leak the secret" "$out" "$SECRET"
}
cred_case 'without BACKSOON_ADMIN_USER' BACKSOON_ADMIN_USER -e BACKSOON_ADMIN_PASSWORD=pw-for-test -e "BACKSOON_SESSION_SECRET=$SECRET"
cred_case 'without any password' BACKSOON_ADMIN_PASSWORD_HASH -e BACKSOON_ADMIN_USER=admin -e "BACKSOON_SESSION_SECRET=$SECRET"
cred_case 'without BACKSOON_SESSION_SECRET' BACKSOON_SESSION_SECRET -e BACKSOON_ADMIN_USER=admin -e BACKSOON_ADMIN_PASSWORD=pw-for-test
cred_case 'with a 5-byte BACKSOON_SESSION_SECRET' BACKSOON_SESSION_SECRET -e BACKSOON_ADMIN_USER=admin -e BACKSOON_ADMIN_PASSWORD=pw-for-test -e BACKSOON_SESSION_SECRET=short
cred_case 'with an invalid BACKSOON_TRUSTED_PROXIES' BACKSOON_TRUSTED_PROXIES -e BACKSOON_ADMIN_USER=admin -e BACKSOON_ADMIN_PASSWORD=pw-for-test -e "BACKSOON_SESSION_SECRET=$SECRET" -e BACKSOON_TRUSTED_PROXIES=not-a-range

section 'language: Accept-Language, the ES/EN selector and its cookie (no session needed)'
# html_lang: the lang attribute of the <html> element of the last BODY.
html_lang() { printf '%s\n' "$BODY" | grep -o '<html lang="[^"]*"' | head -n 1 | sed 's/.*lang="//; s/"$//'; }
anon /login
expect_eq 'no language signal: the login is English' en "$(html_lang)"
expect_eq 'a localized response varies on Accept-Language and Cookie' 'Accept-Language, Cookie' "$(header_value Vary)"
anon /login -H 'Accept-Language: es-ES,es;q=0.9,en;q=0.8'
expect_eq 'Accept-Language es through NPM: the login is Spanish' es "$(html_lang)"
expect_has 'the Spanish login offers the switch to English' "$BODY" '/lang?to=en'
anon /login -H 'Accept-Language: en-US,en;q=0.9,es;q=0.8'
expect_eq 'Accept-Language en first: the login is English' en "$(html_lang)"
anon /login -H 'Accept-Language: fr-FR,fr;q=0.9'
expect_eq 'Accept-Language with no supported language: English' en "$(html_lang)"
anon '/lang?to=en&next=%2Flogin' -H 'Accept-Language: es'
expect_eq 'the selector answers without a session -> 303' 303 "$STATUS"
expect_eq 'the selector returns to the page asked for' /login "$(header_value Location)"
lang_line=$(set_cookie_line backsoon_lang)
expect_has 'the selector sets backsoon_lang' "$lang_line" 'backsoon_lang=en'
expect_has 'the language cookie is site-wide' "$lang_line" 'Path=/;'
expect_has 'the language cookie is HttpOnly' "$lang_line" 'HttpOnly'
expect_has 'the language cookie is Secure' "$lang_line" 'Secure'
expect_has 'the language cookie is SameSite=Strict' "$lang_line" 'SameSite=Strict'
lang_age=$(printf '%s\n' "$lang_line" | sed -n 's/.*Max-Age=\([0-9]*\).*/\1/p')
case $lang_age in
  ''|*[!0-9]*) ko 'the language cookie lasts about a year' "Max-Age [$lang_age]" ;;
  *) if [ "$lang_age" -ge 31000000 ] && [ "$lang_age" -le 32000000 ]; then ok "the language cookie lasts about a year (Max-Age=$lang_age)"; else ko 'the language cookie lasts about a year' "Max-Age [$lang_age]"; fi ;;
esac
anon /login -H 'Accept-Language: es' -H 'Cookie: backsoon_lang=en'
expect_eq 'the cookie beats Accept-Language (English)' en "$(html_lang)"
anon '/lang?to=es&next=%2Flogin'
expect_has 'the selector sets Spanish' "$(set_cookie_line backsoon_lang)" 'backsoon_lang=es'
anon /login -H 'Cookie: backsoon_lang=es'
expect_eq 'the cookie alone selects Spanish' es "$(html_lang)"
anon /login -H 'Accept-Language: es' -H 'Cookie: backsoon_lang=fr'
expect_eq 'an unsupported cookie value is ignored' es "$(html_lang)"
for bad in '%2F%2Fevil.test' '%2F%5Cevil.test' 'https%3A%2F%2Fevil.test' ''; do
  anon "/lang?to=es&next=$bad"
  expect_eq "selector next=[$bad] returns to / (no open redirect)" / "$(header_value Location)"
done
anon '/lang?to=fr&next=%2Flogin'
expect_eq 'an unknown language -> 303' 303 "$STATUS"
expect_eq 'an unknown language sets no cookie' 0 "$(set_cookie_line backsoon_lang | wc -l | tr -d ' ')"
anon /lang -X POST --data-urlencode to=es
expect_eq 'POST /lang is not a selector route' 0 "$(set_cookie_line backsoon_lang | wc -l | tr -d ' ')"

section 'login through the real form'
anon /login
expect_eq 'GET /login -> 200' 200 "$STATUS"
login_line=$(set_cookie_line backsoon_login)
expect_has 'login cookie is HttpOnly' "$login_line" 'HttpOnly'
expect_has 'login cookie is Secure' "$login_line" 'Secure'
expect_has 'login cookie is SameSite=Strict' "$login_line" 'SameSite=Strict'
expect_has 'login cookie is scoped to /login' "$login_line" 'Path=/login'
LOGIN_COOKIE=$(set_cookie_value backsoon_login)
LOGIN_TOKEN=$(field_value login_token)
[ -n "$LOGIN_COOKIE" ] && [ -n "$LOGIN_TOKEN" ] || die 'login form lacks its token cookie or login_token field'
expect_has 'form has a user field' "$BODY" 'name="user"'
expect_has 'form has a password field' "$BODY" 'name="password"'
# try_login USER PASSWORD [extra curl args]: the login form as a browser would post it.
try_login() {
  _u=$1; _w=$2; shift 2
  anon /login -X POST -H "Cookie: backsoon_login=$LOGIN_COOKIE" --data-urlencode "login_token=$LOGIN_TOKEN" \
    --data-urlencode "user=$_u" --data-urlencode "password=$_w" "$@"
}
anon /login -X POST --data-urlencode "login_token=$LOGIN_TOKEN" --data-urlencode user=admin --data-urlencode "password=$PASSWORD"
expect_eq 'login without the token cookie -> 403' 403 "$STATUS"
expect_eq 'login without the token cookie sets no session' 0 "$(set_cookie_line backsoon_session | wc -l)"
anon /login -X POST -H "Cookie: backsoon_login=$LOGIN_COOKIE" --data-urlencode user=admin --data-urlencode "password=$PASSWORD"
expect_eq 'login without the login_token field -> 403' 403 "$STATUS"
try_login admin "$PASSWORD" -H 'Sec-Fetch-Site: cross-site'
expect_eq 'cross-site login (Sec-Fetch-Site) -> 403' 403 "$STATUS"
expect_eq 'cross-site login sets no session' 0 "$(set_cookie_line backsoon_session | wc -l)"
try_login admin wrong-password-xyz
expect_eq 'wrong password -> 401' 401 "$STATUS"
expect_eq 'wrong password sets no session' 0 "$(set_cookie_line backsoon_session | wc -l)"
try_login nobody "$PASSWORD"
expect_eq 'wrong user -> 401' 401 "$STATUS"
expect_eq 'wrong user sets no session' 0 "$(set_cookie_line backsoon_session | wc -l)"
try_login admin "$PASSWORD"
expect_eq 'correct password -> 303' 303 "$STATUS"
expect_eq 'login redirects to the directory' / "$(header_value Location)"
session_line=$(set_cookie_line backsoon_session)
expect_has 'session cookie is HttpOnly' "$session_line" 'HttpOnly'
expect_has 'session cookie is Secure' "$session_line" 'Secure'
expect_has 'session cookie is SameSite=Strict' "$session_line" 'SameSite=Strict'
max_age=$(printf '%s\n' "$session_line" | sed -n 's/.*Max-Age=\([0-9]*\).*/\1/p')
case $max_age in
  ''|*[!0-9]*) ko 'session cookie expires in 12 h' "Max-Age [$max_age]" ;;
  *) if [ "$max_age" -ge 43190 ] && [ "$max_age" -le 43200 ]; then ok "session cookie expires in 12 h (Max-Age=$max_age)"; else ko 'session cookie expires in 12 h' "Max-Age [$max_age]"; fi ;;
esac
SESSION=$(set_cookie_value backsoon_session)
[ -n "$SESSION" ] || die 'login did not set a session cookie'
COOKIE="backsoon_session=$SESSION"
list
expect_eq 'the directory renders for the session' 200 "$STATUS"
panel / -H 'Accept: text/html' -H 'Accept-Language: es'
expect_eq 'the signed-in directory follows Accept-Language: es' es "$(html_lang)"
expect_has 'the navbar offers the switch to English' "$BODY" '/lang?to=en'
list
expect_eq 'rejected unauthenticated POSTs left only the default page' 1 "$(page_hrefs | wc -l | tr -d ' ')"
expect_not_listed 'the rejected unauthenticated page is not listed' Ghost-e2e
expect_listed 'the default page is listed' '/pages/default'
panel /pages/new -H 'Accept: text/html'
expect_eq 'the new-page form renders' 200 "$STATUS"
CSRF=$(field_value csrf)
[ -n "$CSRF" ] || die 'the new-page form has no csrf field'

section 'CA-18: three pages with varied destinations, then the directory'
create_template 'Shop-e2e' 'shop.test, shop2.test' 'Shop App'
expect_created 'template page Shop (two domains)'
SHOP=$PAGE_ID
create_upload 'Billing-e2e' 'billing:8443' "$FIX/upload-valid.html"
expect_created 'uploaded page Billing (service:port)'
BILLING=$PAGE_ID
create_template 'Blog-e2e' 'blog.test
blog-admin:3000' 'Blog App'
expect_created 'template page Blog (domain + service:port)'
BLOG=$PAGE_ID
list
expect_eq 'the directory lists the 3 pages plus the default page' 4 "$(page_hrefs | wc -l | tr -d ' ')"
today=$(date -u +%F)
expect_ge 'the directory shows a modification date for every page' "$(printf '%s\n' "$BODY" | grep -o "$today" | wc -l | tr -d ' ')" 4
for pair in Shop-e2e:$SHOP Billing-e2e:$BILLING Blog-e2e:$BLOG Default\ page:default; do
  name=${pair%%:*}; id=${pair#*:}
  expect_listed "$name is listed" "$name"
  expect_line "$name links to its page" "$(page_hrefs)" "/pages/$id"
  case $BODY in
    *"/pages/$id/preview"*|*"focus=$id"*) ok "$name has a preview link" ;;
    *) ko "$name has a preview link" "no /pages/$id/preview or focus=$id in the directory" ;;
  esac
done
for value in shop.test shop2.test billing:8443 blog.test blog-admin:3000; do expect_listed "destination $value is listed" "$value"; done
edit_page "$SHOP";    expect_has 'Shop owns shop.test' "$BODY" shop.test;     expect_has 'Shop owns shop2.test' "$BODY" shop2.test
edit_page "$BILLING"; expect_has 'Billing owns billing:8443' "$BODY" billing:8443
edit_page "$BLOG";    expect_has 'Blog owns blog.test' "$BODY" blog.test;     expect_has 'Blog owns blog-admin:3000' "$BODY" blog-admin:3000
served shop.test
expect_maintenance 'Shop domain page published' 10
expect_has 'backsoon serves Shop' "$BODY" 'Shop App'
served shop2.test
expect_has 'backsoon serves Shop for the second domain' "$BODY" 'Shop App'
served nowhere.test billing:8443
expect_has 'backsoon serves Billing for the service:port' "$BODY" 'fixture-admin-upload'
served blog.test
expect_has 'backsoon serves Blog for its domain' "$BODY" 'Blog App'
served other.test blog-admin:3000
expect_has 'backsoon serves Blog for its service:port' "$BODY" 'Blog App'

section 'CA-19: uploads and the template'
create_upload 'Upload-e2e' 'upload.test' "$FIX/upload-valid.html"
expect_created 'valid .html upload'
served upload.test
expect_has 'valid upload is served by backsoon' "$BODY" 'fixture-admin-upload'
pages_before=$(page_count)
{ printf '<!DOCTYPE html><html><head><meta charset="utf-8"><title>big</title></head><body>'; head -c 614400 /dev/zero | tr '\0' 'a'; printf '</body></html>\n'; } > "$ARTIFACTS/big.html"
create_upload 'Big-e2e' 'big.test' "$ARTIFACTS/big.html"
expect_eq '600 KB upload -> 413' 413 "$STATUS"
list
expect_eq '600 KB upload created nothing' "$pages_before" "$(page_hrefs | wc -l | tr -d ' ')"
expect_not_listed '600 KB upload is not listed' Big-e2e
expect_default '600 KB upload' big.test
printf '<!DOCTYPE html><html><body><p>\377\376 not utf-8</p></body></html>\n' > "$ARTIFACTS/latin1.html"
create_upload 'Latin-e2e' 'latin.test' "$ARTIFACTS/latin1.html"
expect_eq 'non-UTF-8 upload -> 422' 422 "$STATUS"
list
expect_eq 'non-UTF-8 upload created nothing' "$pages_before" "$(page_hrefs | wc -l | tr -d ' ')"
expect_not_listed 'non-UTF-8 upload is not listed' Latin-e2e
expect_default 'non-UTF-8 upload' latin.test
printf 'plain text, not html\n' > "$ARTIFACTS/notes.txt"
panel /pages -X POST -F "csrf=$CSRF" -F name=Notes-e2e -F 'destinations=notes.test' -F source=upload -F "file=@$ARTIFACTS/notes.txt;type=text/plain"
expect_eq 'upload that is not .html -> 422' 422 "$STATUS"
expect_default 'upload that is not .html' notes.test
create_template 'Fish-e2e' 'fish.test' 'Fish & <b>Chips</b>'
expect_created 'template page with markup in the application name'
FISH=$PAGE_ID
panel "/pages/$FISH/preview"
expect_eq 'template page preview -> 200' 200 "$STATUS"
expect_has 'application name is HTML-escaped in the page' "$BODY" 'Fish &amp; &lt;b&gt;Chips&lt;/b&gt;'
expect_lacks 'raw markup from the application name is not injected' "$BODY" '<b>Chips</b>'
expect_has 'refresh meta carries the refresh interval' "$BODY" 'content="10"'
expect_has 'language comes from the form' "$BODY" '<html lang="en">'
expect_lacks 'no template placeholder is left' "$BODY" '{{'
served fish.test
expect_maintenance 'template page served by backsoon' 10
expect_has 'backsoon serves the escaped application name' "$BODY" 'Fish &amp; &lt;b&gt;Chips&lt;/b&gt;'
expect_has 'backsoon serves the refresh meta' "$BODY" 'content="10"'
create_template 'NoApp-e2e' 'noapp.test' ''
expect_eq 'template without an application name -> 422' 422 "$STATUS"
expect_default 'template without an application name' noapp.test
list
expect_not_listed 'template without an application name is not listed' NoApp-e2e

section 'CA-20: destinations are normalized, validated and exclusive'
create_template 'Norm-e2e' 'APP.Test' 'Norm App'
expect_created 'APP.Test is accepted'
NORM=$PAGE_ID
list
expect_listed 'APP.Test is listed as app.test' app.test
expect_not_listed 'the original casing is not listed' APP.Test
edit_page "$NORM"
expect_has 'APP.Test is stored as app.test' "$BODY" app.test
expect_lacks 'the original casing is not kept' "$BODY" APP.Test
served app.test
expect_has 'backsoon serves the normalized domain' "$BODY" 'Norm App'
pages_before=$(page_count)
create_template 'BadPort-e2e' 'app:99999' 'Bad Port App'
expect_eq 'app:99999 -> 422' 422 "$STATUS"
expect_has 'the rejected destination is shown back to the user' "$BODY" 'app:99999'
expect_has 'the form is shown again with its destinations field' "$BODY" 'name="destinations"'
create_template 'BadSpace-e2e' 'a b' 'Bad Space App'
expect_eq '"a b" -> 422' 422 "$STATUS"
expect_has 'the rejected destination "a b" is shown back to the user' "$BODY" 'a b'
list
expect_eq 'rejected destinations created nothing' "$pages_before" "$(page_hrefs | wc -l | tr -d ' ')"
expect_not_listed 'BadPort is not listed' BadPort-e2e
expect_not_listed 'BadSpace is not listed' BadSpace-e2e
create_template 'Thief-e2e' 'app.test' 'Thief App'
expect_eq 'already-owned destination asks for confirmation (409)' 409 "$STATUS"
expect_has 'confirmation names the destination' "$BODY" 'app.test'
expect_has 'confirmation names the current owner' "$BODY" 'Norm-e2e'
expect_has 'confirmation can be submitted (draft field)' "$BODY" 'name="draft"'
list
expect_not_listed 'unconfirmed move created nothing' Thief-e2e
edit_page "$NORM"
expect_has 'the owner keeps the destination' "$BODY" app.test
served app.test
expect_has 'backsoon still serves the previous owner' "$BODY" 'Norm App'
create_template 'Thief-e2e' 'app.test' 'Thief App' --data-urlencode confirm=true
expect_created 'confirm=true moves the destination'
THIEF=$PAGE_ID
edit_page "$THIEF"
expect_has 'the new page owns the destination' "$BODY" app.test
edit_page "$NORM"
expect_lacks 'the previous owner no longer owns it' "$BODY" app.test
list
expect_listed 'the previous owner is still listed' Norm-e2e
served app.test
expect_has 'backsoon serves the new owner' "$BODY" 'Thief App'
expect_lacks 'backsoon no longer serves the previous owner' "$BODY" 'Norm App'

section 'CA-14 (panel part): external resources need a confirmation'
pages_before=$(page_count)
create_upload 'Ext-e2e' 'ext.test' "$FIX/external-link.html"
case $STATUS in
  200|409|422) ok "HTML with <link href=\"https://...\"> asks for confirmation ($STATUS, no redirect)" ;;
  *) ko 'HTML with <link href="https://..."> asks for confirmation' "status $STATUS" ;;
esac
expect_eq 'the warning response creates no redirect' 0 "$(header_count Location)"
expect_has 'the confirmation can be submitted (confirm field)' "$BODY" 'name="confirm"'
list
expect_eq 'nothing is created before confirming' "$pages_before" "$(page_hrefs | wc -l | tr -d ' ')"
expect_not_listed 'Ext is not listed yet' Ext-e2e
expect_default 'unconfirmed external-resource page' ext.test
create_upload 'Ext-e2e' 'ext.test' "$FIX/external-link.html" -F confirm=true
expect_created 'confirm=true creates the page'
served ext.test
expect_has 'the confirmed page is served by backsoon' "$BODY" 'fixture-admin-external'

section 'CA-21: sandboxed preview and CSRF'
create_upload 'Script-e2e' 'script.test' "$FIX/xss.html" -F confirm=true
expect_created 'page with <script>alert(1)</script> (one-shot confirm=true)'
XSS=$PAGE_ID
panel "/pages/$XSS/preview"
expect_eq 'preview -> 200' 200 "$STATUS"
expect_header 'preview CSP is exactly the sandbox policy' Content-Security-Policy "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"
expect_header 'preview is not sniffed' X-Content-Type-Options nosniff
expect_header 'preview is not cached' Cache-Control no-store
expect_has 'the preview serves the uploaded HTML untouched, under that policy' "$BODY" '<script>alert(1)</script>'
iframe_views=0
for view in / "/?focus=$XSS" "/pages/$XSS" /pages/new; do
  panel "$view" -H 'Accept: text/html'
  expect_eq "panel view $view is served" 200 "$STATUS"
  expect_lacks "panel view $view never contains the uploaded script" "$BODY" 'alert(1)'
  if [ -n "$(header_value Content-Security-Policy)" ]; then ok "panel view $view sends a CSP"; else ko "panel view $view sends a CSP"; fi
  # Whatever the view embeds, every iframe must be sandboxed and must not allow scripts.
  frames=$(printf '%s\n' "$BODY" | grep -o '<iframe[^>]*>')
  if [ -n "$frames" ]; then
    expect_eq "panel view $view: every iframe is sandboxed" 0 "$(printf '%s\n' "$frames" | grep -vc ' sandbox')"
    expect_lacks "panel view $view: no iframe allows scripts" "$frames" 'allow-scripts'
    case $frames in *"/pages/$XSS/preview"*) iframe_views=$((iframe_views + 1)) ;; esac
  fi
done
expect_ge 'at least one panel view embeds the preview in an iframe' "$iframe_views" 1
pages_before=$(page_count)
panel /pages -X POST --data-urlencode name=NoCsrf-e2e --data-urlencode destinations=nocsrf.test --data-urlencode source=template --data-urlencode app_name=NoCsrf
expect_eq 'POST /pages without CSRF -> 403' 403 "$STATUS"
panel /pages -X POST --data-urlencode csrf=forged --data-urlencode name=BadCsrf-e2e --data-urlencode destinations=badcsrf.test --data-urlencode source=template --data-urlencode app_name=BadCsrf
expect_eq 'POST /pages with a wrong CSRF -> 403' 403 "$STATUS"
panel /pages -X POST -F csrf=forged -F confirm=true -F name=BadCsrfUp-e2e -F destinations=badcsrf2.test -F source=upload -F "file=@$FIX/upload-valid.html;type=text/html"
expect_eq 'multipart POST with a wrong CSRF and confirm=true -> 403' 403 "$STATUS"
panel "/pages/$FISH" -X POST --data-urlencode csrf=forged --data-urlencode name=Hijacked-e2e --data-urlencode destinations=fish.test
expect_eq 'POST /pages/{id} with a wrong CSRF -> 403' 403 "$STATUS"
panel "/pages/$FISH/delete" -X POST --data-urlencode confirm=true
expect_eq 'delete without CSRF (even with confirm=true) -> 403' 403 "$STATUS"
panel "/pages/$FISH/delete" -X POST --data-urlencode csrf=forged --data-urlencode confirm=true
expect_eq 'delete with a wrong CSRF -> 403' 403 "$STATUS"
panel /logout -X POST --data-urlencode csrf=forged
expect_eq 'logout with a wrong CSRF -> 403' 403 "$STATUS"
list
expect_eq 'no forged request created or deleted a page' "$pages_before" "$(page_hrefs | wc -l | tr -d ' ')"
for name in NoCsrf-e2e BadCsrf-e2e BadCsrfUp-e2e Hijacked-e2e; do expect_not_listed "$name is not listed" "$name"; done
expect_listed 'Fish survived the forged delete under its own name' Fish-e2e
served fish.test
expect_has 'Fish is still served' "$BODY" 'Fish &amp;'
expect_default 'forged create' badcsrf.test
expect_default 'forged multipart create' badcsrf2.test

section 'CA-6 (T4): create, modify and delete from the panel, visible on the next request'
probe app1.test /
expect_eq 'app1 is healthy before its page exists' 200 "$STATUS"
create_template 'Live-e2e' 'app1.test' 'Live App One'
expect_created 'page for app1.test'
LIVE=$PAGE_ID
stop_service app1
probe app1.test '/some/path?x=1'
expect_maintenance 'app1 stopped: the panel page is served through NPM' 10
expect_has 'the page created in the panel is the maintenance page' "$BODY" 'Live App One'
expect_has 'the page refreshes at the configured interval' "$BODY" 'content="10"'
panel "/pages/$LIVE" -X POST -F "csrf=$CSRF" -F name=Live-e2e -F destinations=app1.test -F "file=@$FIX/replacement.html;type=text/html"
expect_eq 'replacing the HTML -> 303' 303 "$STATUS"
probe app1.test /
expect_maintenance 'next request after the HTML replacement' 10
expect_has 'the new HTML is served' "$BODY" 'fixture-admin-replacement'
expect_lacks 'the old HTML is gone' "$BODY" 'Live App One'
panel "/pages/$LIVE/delete" -X POST --data-urlencode "csrf=$CSRF"
expect_eq 'delete without confirmation answers with a confirmation (200)' 200 "$STATUS"
expect_has 'the delete confirmation names the page' "$BODY" Live-e2e
expect_has 'the delete confirmation names its destination' "$BODY" app1.test
probe app1.test /
expect_has 'an unconfirmed delete keeps the page' "$BODY" 'fixture-admin-replacement'
panel "/pages/$LIVE/delete" -X POST --data-urlencode "csrf=$CSRF" --data-urlencode confirm=true
expect_eq 'confirmed delete -> 303' 303 "$STATUS"
probe app1.test /
expect_maintenance 'next request after delete' 10
expect_has 'delete releases the destination to default.html' "$BODY" "$DEFAULT_MARK"
expect_lacks 'the deleted page is gone' "$BODY" 'fixture-admin-replacement'
list
expect_not_listed 'Live is no longer listed' Live-e2e
panel "/pages/$LIVE/preview"
expect_eq 'the deleted page has no preview (404)' 404 "$STATUS"
create_template 'Svc-e2e' 'app1:80' 'Service App'
expect_created 'page for the service:port app1:80'
SVC=$PAGE_ID
probe app1.test /
expect_maintenance 'service:port page through NPM' 10
expect_has 'with no domain page, the app1:80 page is served (actual NPM $server:$port)' "$BODY" 'Service App'
create_template 'Live2-e2e' 'app1.test' 'Domain App'
expect_created 'domain page added on top of the service:port page'
LIVE2=$PAGE_ID
probe app1.test /
expect_has 'the domain page wins over the service:port page' "$BODY" 'Domain App'
panel "/pages/$LIVE2/delete" -X POST --data-urlencode "csrf=$CSRF" --data-urlencode confirm=true
expect_eq 'domain page deleted -> 303' 303 "$STATUS"
probe app1.test /
expect_has 'after deleting the domain page the service:port page is served again' "$BODY" 'Service App'
panel "/pages/$SVC/delete" -X POST --data-urlencode "csrf=$CSRF" --data-urlencode confirm=true
expect_eq 'service:port page deleted -> 303' 303 "$STATUS"
probe app1.test /
expect_has 'after deleting every page, default.html' "$BODY" "$DEFAULT_MARK"
panel /pages/default/delete -X POST --data-urlencode "csrf=$CSRF" --data-urlencode confirm=true
expect_eq 'the default page cannot be deleted (409)' 409 "$STATUS"
probe app1.test /
expect_has 'default.html is still served after the refused delete' "$BODY" "$DEFAULT_MARK"
expect_eq 'nothing restarted: backsoon' "$SERVICE_STARTED" "$(started_at backsoon)"
expect_eq 'nothing restarted: the panel' "$PANEL_STARTED" "$(started_at backsoon-admin)"
# One panel page stays for the degradation checks below; app1 is still stopped.
create_template 'Keep-e2e' 'app1.test' 'Kept App'
expect_created 'page for app1.test kept for the degradation checks'
KEEP=$PAGE_ID

section 'volume ownership: the non-root panel manages what the root init created'
own=$(dc exec -T backsoon stat -c '%u:%g %a %n' /pages /pages/host /pages/target /pages/default.html /pages/host/app1.test.html /pages/target/billing_8443.html 2>&1)
printf '%s\n' "$own" > "$ARTIFACTS/volume-ownership.txt"
expect_line '/pages is owned by 65532 (755)' "$own" '65532:65532 755 /pages'
expect_line '/pages/host is owned by 65532 (755)' "$own" '65532:65532 755 /pages/host'
expect_line '/pages/target is owned by 65532 (755)' "$own" '65532:65532 755 /pages/target'
expect_line 'default.html is owned by 65532 (644)' "$own" '65532:65532 644 /pages/default.html'
expect_line 'a published host file is 644' "$own" '65532:65532 644 /pages/host/app1.test.html'
expect_line 'a published target file is 644' "$own" '65532:65532 644 /pages/target/billing_8443.html'
expect_eq 'backsoon mounts the pages volume read-only' false "$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/pages"}}{{.RW}}{{end}}{{end}}' "$(dc ps -q backsoon)")"
served billing-unused.test billing:8443
expect_has 'the nginx worker reads a file the panel created' "$BODY" 'fixture-admin-upload'
expect_eq 'the panel runs as the non-root uid' 65532:65532 "$(docker inspect -f '{{.Config.User}}' "$(dc ps -q backsoon-admin)")"
expect_eq 'the panel root filesystem is read-only' true "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$(dc ps -q backsoon-admin)")"
dc exec -T backsoon-admin /usr/local/bin/backsoon-admin -healthcheck >/dev/null 2>&1
expect_eq 'the -healthcheck flag exits 0 inside the container' 0 "$?"
expect_eq 'no publish failure was logged' 0 "$(dc logs --no-log-prefix backsoon-admin 2>&1 | grep -c 'publish failed')"

section 'RNF-3 / RNF-2: health, image size and memory of the panel'
expect_eq 'panel healthcheck is healthy' healthy "$(health_of backsoon-admin)"
size_bytes=$(docker image inspect -f '{{.Size}}' "$ADMIN_IMAGE")
docker image ls "$ADMIN_IMAGE" | tee "$ARTIFACTS/image-ls.txt"
if [ "$size_bytes" -lt $((30 * 1000 * 1000)) ]; then ok "panel image is under 30 MB ($size_bytes bytes)"; else ko 'panel image is under 30 MB' "$size_bytes bytes"; fi
stats=$(docker stats --no-stream --format '{{.MemUsage}}' "$(dc ps -q backsoon-admin)")
printf 'panel memory (docker stats --no-stream): %s\n' "$stats" | tee "$ARTIFACTS/stats.txt"
mem_mib=$(printf '%s\n' "$stats" | awk '{ v = $1; u = v; sub(/[0-9.]+/, "", u); sub(/[A-Za-z]+$/, "", v); m = (u == "KiB") ? v / 1024 : (u == "MiB") ? v : (u == "GiB") ? v * 1024 : (u == "B") ? v / 1048576 : 999999; printf "%.1f", m }')
if awk -v m="$mem_mib" 'BEGIN { exit !(m < 50) }'; then ok "panel memory is under 50 MiB (${mem_mib} MiB)"; else ko 'panel memory is under 50 MiB' "${mem_mib} MiB"; fi

section 'RNF-4(a) / CA-16 (panel part): with the panel stopped, backsoon keeps serving'
pages_before_stop=$(page_count)
stop_service backsoon-admin
probe app1.test /
expect_maintenance 'panel stopped, app1 stopped: maintenance through NPM' 10
expect_has 'the page published by the panel is still served' "$BODY" 'Kept App'
probe app1.test / -X POST -d 'a=b'
expect_maintenance 'panel stopped: POST also served' 10
served nowhere.test billing:8443
expect_has 'panel stopped: backsoon serves a service:port page' "$BODY" 'fixture-admin-upload'
probe app2.test /
expect_eq 'panel stopped: other applications unaffected' 200 "$STATUS"
expect_has 'panel stopped: app2 serves whoami' "$BODY" 'Name: app2'
anon /healthz
expect_eq 'panel stopped: its own host falls back to maintenance' 503 "$STATUS"
expect_header 'the fallback for the stopped panel is marked' X-Backsoon 1
dc exec -T npm nginx -t || die 'NPM config invalid with the panel stopped'
dc start backsoon-admin || die 'cannot restart the panel'
wait_for 'panel healthy again' "$BACKSOON_TEST_WAIT" panel_healthy
wait_for 'panel through NPM again' "$BACKSOON_TEST_WAIT" panel_up
list
expect_eq 'the session survives a panel restart' 200 "$STATUS"
expect_eq 'the restarted panel lists the same pages (sites.json)' "$pages_before_stop" "$(page_hrefs | wc -l | tr -d ' ')"
for name in Shop-e2e Billing-e2e Blog-e2e Upload-e2e Fish-e2e Norm-e2e Thief-e2e Ext-e2e Script-e2e Keep-e2e; do
  expect_listed "restart keeps $name" "$name"
done
probe app1.test /
expect_has 'the republished page is served after the restart' "$BODY" 'Kept App'
panel "/pages/$KEEP/delete" -X POST --data-urlencode "csrf=$CSRF" --data-urlencode confirm=true
expect_eq 'cleanup: Keep deleted' 303 "$STATUS"
dc start app1 || die 'cannot restore app1'
wait_for 'app1 back through NPM' 30 app_probe app1.test 200 'Name: app1'
expect_eq 'app1 recovers' 200 "$STATUS"
expect_eq 'recovered app1 has no maintenance header' 0 "$(header_count X-Backsoon)"

section 'CA-24: the sixth failed login from one client is refused, even with the right password'
for attempt in 1 2 3 4 5; do
  try_login admin wrong-password-xyz
  expect_eq "failed login $attempt -> 401" 401 "$STATUS"
done
try_login admin "$PASSWORD"
expect_eq 'sixth attempt with the CORRECT password -> 429' 429 "$STATUS"
retry_after=$(header_value Retry-After)
case $retry_after in
  ''|*[!0-9]*) ko 'Retry-After is a number of seconds' "[$retry_after]" ;;
  *) if [ "$retry_after" -gt 0 ] && [ "$retry_after" -le 900 ]; then ok "Retry-After is 1-900 s ($retry_after)"; else ko 'Retry-After is 1-900 s' "[$retry_after]"; fi ;;
esac
expect_eq 'blocked login sets no session' 0 "$(set_cookie_line backsoon_session | wc -l)"
try_login admin "$PASSWORD"
expect_eq 'the block holds on the next attempt' 429 "$STATUS"
panel / -H 'Accept: text/html'
expect_eq 'an already-issued session keeps working' 200 "$STATUS"

section 'RNF-9: the panel logs logins and every change, never a password'
logs=$(dc logs --no-log-prefix backsoon-admin 2>&1)
printf '%s\n' "$logs" > "$ARTIFACTS/panel.log"
log_has() { if printf '%s\n' "$logs" | grep -i "$2" | grep -q "$3"; then ok "$1"; else ko "$1" "no [$2] line with [$3]"; fi; }
log_has 'log: login ok names the user' 'login ok' 'admin'
log_has 'log: login failed' 'login failed' 'ip='
log_has 'log: login blocked' 'login blocked' 'ip='
log_has 'log: page created (Shop) with its destinations' 'page created' 'Shop-e2e.*shop.test'
log_has 'log: page created (normalized destination)' 'page created' 'Norm-e2e.*app.test'
log_has 'log: page updated (HTML replaced)' 'page updated' 'Live-e2e'
log_has 'log: page deleted (Live) with its destination' 'page deleted' 'Live-e2e.*app1.test'
expect_lacks 'log: no wrong password' "$logs" 'wrong-password-xyz'
expect_lacks 'log: no real password' "$logs" "$PASSWORD"
expect_lacks 'log: no session secret' "$logs" "$SECRET"
expect_lacks 'log: no session cookie' "$logs" "$SESSION"
client_ip=$(printf '%s\n' "$logs" | sed -n 's/.*login ok.* ip=\([0-9a-f.:]*\).*/\1/p' | head -n 1)
case $client_ip in
  ''|127.0.0.1|::1) ko 'log: the client address is the one NPM forwarded (X-Real-IP)' "[$client_ip]" ;;
  *) ok "log: the client address is the one NPM forwarded ($client_ip)" ;;
esac

section 'Logout revokes the session: a copy of the old cookie no longer opens the panel'
panel /logout -X POST --data-urlencode "csrf=$CSRF"
expect_eq 'logout -> 303' 303 "$STATUS"
expect_eq 'logout redirects to /login' /login "$(header_value Location)"
expect_has 'logout clears the session cookie' "$(set_cookie_line backsoon_session)" 'Max-Age=0'
panel / -H 'Accept: text/html' -H 'Sec-Fetch-Mode: navigate'
expect_eq 'the old cookie after logout -> 303' 303 "$STATUS"
expect_eq 'the old cookie after logout is sent to /login' /login "$(header_value Location)"
panel / -H 'Accept: */*'
expect_eq 'the old cookie after logout, bare client -> 401' 401 "$STATUS"

section 'BACKSOON_TRUSTED_PROXIES: X-Real-IP is believed only from the listed peers'
# NPM's address on the network it shares with the panel: the peer the panel sees.
npm_peer=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{$v.IPAddress}}{{"\n"}}{{end}}' "$(dc ps -q npm)" |
  awk '$1 ~ /_testnet$/ { print $2 }')
[ -n "$npm_peer" ] || die 'cannot find the NPM address on the test network'
[ "$npm_peer" != "$client_ip" ] || die 'the NPM address equals the forwarded client address: the check would prove nothing'
# with_trusted VALUE: recreate the panel with BACKSOON_TRUSTED_PROXIES=VALUE and sign in once.
with_trusted() {
  BACKSOON_TEST_TRUSTED_PROXIES=$1 dc up -d --no-deps --force-recreate backsoon-admin || die 'cannot recreate the panel'
  reload
  wait_for 'recreated panel healthy' "$BACKSOON_TEST_WAIT" panel_healthy
  wait_for 'recreated panel through NPM' "$BACKSOON_TEST_WAIT" panel_up
  anon /login
  LOGIN_COOKIE=$(set_cookie_value backsoon_login)
  LOGIN_TOKEN=$(field_value login_token)
  [ -n "$LOGIN_COOKIE" ] && [ -n "$LOGIN_TOKEN" ] || die 'recreated panel: login form lacks its token'
  try_login admin "$PASSWORD"
  login_status=$STATUS
  trusted_logs=$(dc logs --no-log-prefix backsoon-admin 2>&1)
  logged_ip=$(printf '%s\n' "$trusted_logs" | sed -n 's/.*login ok.* ip=\([0-9a-f.:]*\).*/\1/p' | tail -n 1)
}
with_trusted 192.0.2.0/24
expect_eq 'NPM outside the trusted range: login -> 303' 303 "$login_status"
expect_has 'the start log names the trusted range' "$trusted_logs" 'X-Real-IP trusted from 192.0.2.0/24'
expect_eq 'NPM outside the trusted range: the limiter keys on the NPM address' "$npm_peer" "$logged_ip"
with_trusted "$npm_peer"
expect_eq 'NPM trusted: login -> 303' 303 "$login_status"
expect_has 'the start log names the trusted address' "$trusted_logs" "X-Real-IP trusted from $npm_peer/32"
expect_eq 'NPM trusted: the limiter keys on the X-Real-IP that NPM forwarded' "$client_ip" "$logged_ip"

printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
