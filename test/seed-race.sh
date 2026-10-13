#!/bin/sh
# Focused public-entrypoint regression for concurrent seed publication (B1).
# Usage: sh test/seed-race.sh; creates only bs-unit-* Docker fixtures.
set -u
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT" || exit 2
. "$ROOT/test/service-lib.sh"
. "$ROOT/test/service-cases.sh"
ID="bs-unit-race-$$-$(date +%s)"
IMAGE="$ID-image:test"
IMAGES="$IMAGE"
trap cleanup EXIT
trap 'exit 130' INT TERM
docker build -t "$IMAGE" -f nginx/Dockerfile . || exit 2
case_seed_concurrency || exit 2
printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
