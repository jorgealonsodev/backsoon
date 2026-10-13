#!/bin/sh
# Fail fast on an unusable BACKSOON_REFRESH: it is rendered into nginx config, a response header,
# a JSON body and an HTML attribute, so only a plain integer (1-99999, no leading zero) is accepted.
set -eu

ME=$(basename "$0")
value=${BACKSOON_REFRESH-}

invalid() {
    echo "$ME: ERROR: BACKSOON_REFRESH must be an integer between 1 and 99999" >&2
    exit 1
}

case $value in
    '' | *[!0-9]* | 0*) invalid ;;
esac
[ "${#value}" -le 5 ] || invalid
