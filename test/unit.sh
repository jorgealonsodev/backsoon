#!/bin/sh
# Public-interface tests for the backsoon static nginx service (U2).
#
# Builds nginx/Dockerfile, then drives the container with curl over loopback ephemeral ports
# (no fixed host ports, no compose project, no shared network). Every Docker object is named
# bs-unit-<pid>-<epoch>-* and removed on exit; nothing else is touched.
#
# Usage: sh test/unit.sh        Exit 0 only if every check passes.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 2
. "$ROOT/test/service-lib.sh"
. "$ROOT/test/service-cases.sh"

for tool in docker curl timeout; do
  command -v "$tool" >/dev/null 2>&1 || { echo "setup failed: $tool is required" >&2; exit 2; }
done

ID="bs-unit-$$-$(date +%s)"
IMAGE="$ID-image:test"
trap cleanup EXIT
trap 'exit 130' INT TERM

setup_failed() {
  echo "setup failed: $1" >&2
  exit 2
}

section "setup ($ID)"
IMAGES="$IMAGE"
build_log=$(docker build -t "$IMAGE" -f nginx/Dockerfile . 2>&1) || {
  printf '%s\n' "$build_log" | tail -n 15 >&2
  setup_failed "docker build -f nginx/Dockerfile"
}

# Volumes: PAGES holds hand-made pages on top of the seeded default (refresh 5), DEF is only
# seeded (default refresh), EMPTY is never seeded so the missing-default branch is isolated.
VOL_PAGES="$ID-pages"
VOL_DEF="$ID-def"
VOL_EMPTY="$ID-empty"
for v in "$VOL_PAGES" "$VOL_DEF" "$VOL_EMPTY"; do make_volume "$v" || setup_failed "volume $v"; done
seed_volume "$VOL_PAGES" -e BACKSOON_REFRESH=5 >/dev/null || setup_failed "seeding $VOL_PAGES"
seed_volume "$VOL_DEF" >/dev/null || setup_failed "seeding $VOL_DEF"
vol_sh "$VOL_PAGES" '
  printf "<html><body>HOST-APP1</body></html>"   > /pages/host/app1.test.html
  printf "<html><body>HOST-BOTH</body></html>"   > /pages/host/both.test.html
  printf "<html><body>TARGET-BOTH</body></html>" > /pages/target/10.0.0.5_8081.html
  printf "<html><body>TARGET-ONE</body></html>"  > /pages/target/192.168.1.10_8081.html
' || setup_failed "fixtures in $VOL_PAGES"

R5_NAME="$ID-r5"
DEF_NAME="$ID-def"
NODEF_NAME="$ID-nodef"
LOG_NAME="$ID-log"
start_service "$R5_NAME" "$VOL_PAGES" -e BACKSOON_REFRESH=5 || setup_failed "starting $R5_NAME"
R5_PORT=$PORT
start_service "$DEF_NAME" "$VOL_DEF" || setup_failed "starting $DEF_NAME"
DEF_PORT=$PORT
start_service "$NODEF_NAME" "$VOL_EMPTY" || setup_failed "starting $NODEF_NAME"
NODEF_PORT=$PORT
start_service "$LOG_NAME" "$VOL_PAGES" -e BACKSOON_REFRESH=5 || setup_failed "starting $LOG_NAME"
LOG_PORT=$PORT

run_all_cases

printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
