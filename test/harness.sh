#!/bin/sh
# Lifecycle of the local NPM test stack defined in compose.test.yml.
set -eu

usage() {
	cat <<'EOF'
Usage: sh test/harness.sh <command>

Commands:
  up         start NPM + app1 + app2, install page fixtures, bootstrap NPM via
             its API, then verify app1.test / app2.test through the proxy
  down       remove ONLY this project's containers, networks and volumes
  status     show the project's containers
  check      re-run the direct and proxied probes (exit 0 = healthy)
  bootstrap  re-run test/npm-bootstrap.sh (idempotent)
  fixtures   reset the "pages" volume from test/fixtures/pages
  compose    pass-through: docker compose -f compose.test.yml -p <project> ...
  help       this text

Environment (all optional):
  BACKSOON_TEST_PROJECT     compose project, must start with "backsoon-" (default backsoon-e2e)
  BACKSOON_TEST_HTTP_PORT   127.0.0.1 port for NPM :80   (default 8080)
  BACKSOON_TEST_ADMIN_PORT  127.0.0.1 port for NPM :81   (default 8181)
  BACKSOON_TEST_WITH_BACKSOON=1  also start the optional backsoon service
  BACKSOON_TEST_WAIT        bounded wait per step, seconds (default 180)
  BACKSOON_TEST_NPM_EMAIL / BACKSOON_TEST_NPM_PASSWORD  test-only NPM user

After "up":  curl -H 'Host: app1.test' http://127.0.0.1:$BACKSOON_TEST_HTTP_PORT/
Prerequisites: docker compose v2, curl, python3. See docs/npm-harness.md.
EOF
}

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BACKSOON_TEST_PROJECT=${BACKSOON_TEST_PROJECT:-backsoon-e2e}
BACKSOON_TEST_HTTP_PORT=${BACKSOON_TEST_HTTP_PORT:-8080}
BACKSOON_TEST_ADMIN_PORT=${BACKSOON_TEST_ADMIN_PORT:-8181}
BACKSOON_TEST_WAIT=${BACKSOON_TEST_WAIT:-180}
export BACKSOON_TEST_PROJECT BACKSOON_TEST_HTTP_PORT BACKSOON_TEST_ADMIN_PORT BACKSOON_TEST_WAIT

die() {
	echo "harness: $*" >&2
	exit 1
}

# Guards: the project name scopes every destructive action ("down").
case $BACKSOON_TEST_PROJECT in
backsoon-[a-z0-9]*) ;;
*) die "BACKSOON_TEST_PROJECT must look like backsoon-<name> (got '$BACKSOON_TEST_PROJECT')" ;;
esac
case $BACKSOON_TEST_PROJECT in
*[!a-z0-9_-]*) die "BACKSOON_TEST_PROJECT may only contain a-z 0-9 _ -" ;;
esac
for p in "$BACKSOON_TEST_HTTP_PORT" "$BACKSOON_TEST_ADMIN_PORT"; do
	case $p in
	'' | *[!0-9]*) die "ports must be numeric (got '$p')" ;;
	esac
done
[ "$BACKSOON_TEST_HTTP_PORT" != "$BACKSOON_TEST_ADMIN_PORT" ] || die "HTTP and admin ports must differ"

dc() {
	docker compose -f "$ROOT/compose.test.yml" -p "$BACKSOON_TEST_PROJECT" \
		--profile backsoon --profile tools "$@"
}

need() {
	for tool in docker curl python3; do
		command -v "$tool" >/dev/null 2>&1 || die "missing prerequisite: $tool"
	done
	dc version >/dev/null 2>&1 || die "docker compose v2 is required"
}

diagnostics() {
	echo "harness: --- diagnostics ($BACKSOON_TEST_PROJECT) ---" >&2
	dc ps -a >&2 || true
	dc logs --tail 30 npm app1 app2 >&2 || true
}

# retry <seconds> <description> <command...>: bounded, never loops forever.
retry() {
	limit=$1 what=$2
	shift 2
	end=$(($(date +%s) + limit))
	until "$@" >/dev/null 2>&1; do
		if [ "$(date +%s)" -ge "$end" ]; then
			echo "harness: timed out after ${limit}s waiting for: $what" >&2
			return 1
		fi
		sleep 2
	done
}

port_free() {
	python3 -I -c '
import socket, sys
s = socket.socket()
try:
    s.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(1)' "$1"
}

direct_ok() { dc exec -T npm curl -fsS "http://$1/" | grep -q "^Name: $1\$"; }

# proxied_ok <app>: 200 + whoami body naming the app, via NPM on loopback.
proxied_ok() {
	body=$(curl -sS --max-time 5 -w '\n%{http_code}' -H "Host: $1.test" \
		"http://127.0.0.1:$BACKSOON_TEST_HTTP_PORT/") || return 1
	[ "${body##*
}" = 200 ] || return 1
	printf '%s\n' "$body" | grep -q "^Name: $1\$"
}

check() {
	rc=0
	for app in app1 app2; do
		for probe in direct_ok proxied_ok; do
			if "$probe" "$app"; then
				echo "ok   $probe $app"
			else
				echo "FAIL $probe $app" >&2
				rc=1
			fi
		done
	done
	return $rc
}

cmd_up() {
	need
	echo "harness: project=$BACKSOON_TEST_PROJECT http=127.0.0.1:$BACKSOON_TEST_HTTP_PORT admin=127.0.0.1:$BACKSOON_TEST_ADMIN_PORT"
	if [ -z "$(dc ps -q npm 2>/dev/null)" ]; then
		for p in "$BACKSOON_TEST_HTTP_PORT" "$BACKSOON_TEST_ADMIN_PORT"; do
			port_free "$p" || die "127.0.0.1:$p is already in use; set BACKSOON_TEST_HTTP_PORT / BACKSOON_TEST_ADMIN_PORT"
		done
	fi
	services="npm app1 app2"
	[ "${BACKSOON_TEST_WITH_BACKSOON:-0}" != 1 ] || services="$services backsoon"
	# shellcheck disable=SC2086 # intentional word splitting of the service list
	dc up -d --wait --wait-timeout "$BACKSOON_TEST_WAIT" $services || { diagnostics; die "stack failed to start"; }
	cmd_fixtures
	retry "$BACKSOON_TEST_WAIT" "app1/app2 reachable from npm" sh -c '
		for a in app1 app2; do
			docker compose -f "$0" -p "$1" exec -T npm curl -fsS "http://$a/" || exit 1
		done' "$ROOT/compose.test.yml" "$BACKSOON_TEST_PROJECT" || { diagnostics; die "apps unreachable"; }
	cmd_bootstrap || { diagnostics; die "bootstrap failed"; }
	retry "$BACKSOON_TEST_WAIT" "proxy hosts to serve (app1.test)" proxied_ok app1 &&
		retry "$BACKSOON_TEST_WAIT" "proxy hosts to serve (app2.test)" proxied_ok app2 || { diagnostics; die "proxy not serving"; }
	check || die "post-start check failed"
	echo "harness: ready. Try: curl -H 'Host: app1.test' http://127.0.0.1:$BACKSOON_TEST_HTTP_PORT/"
}

cmd_down() {
	echo "harness: WARNING removing containers, network and VOLUMES (npm db, pages, custom nginx) of project '$BACKSOON_TEST_PROJECT' only." >&2
	need
	dc down -v --remove-orphans --timeout 10
}

cmd_bootstrap() { sh "$ROOT/test/npm-bootstrap.sh"; }

cmd_fixtures() {
	dc run --rm --no-deps -T fixtures >/dev/null
	echo "harness: fixtures installed into the pages volume"
}

cmd=${1:-help}
[ "$#" -eq 0 ] || shift
case $cmd in
up) cmd_up ;;
down) cmd_down ;;
status) need && dc ps -a ;;
check) need && check ;;
bootstrap) need && cmd_bootstrap ;;
fixtures) need && cmd_fixtures ;;
compose) need && dc "$@" ;;
help | -h | --help) usage ;;
*)
	usage >&2
	exit 2
	;;
esac
