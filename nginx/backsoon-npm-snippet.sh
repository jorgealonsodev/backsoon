#!/bin/sh
# backsoon-npm-snippet: installs the global NPM fallback (npm/server_proxy.conf, baked into the
# image) as server_proxy.conf in NPM's /data/nginx/custom directory, mounted here at /npm-custom.
#
# BACKSOON_NPM_CUSTOM_DIR    host path of that directory; unset means: install nothing, exit 0.
# BACKSOON_NPM_INTERCEPT_503 true (default) intercepts 502, 503 and 504; false leaves application
#                            503 responses alone (only 502 and 504).
#
# The file appears atomically (temporary file in the same directory, then rename), and a redeploy
# that would write the same bytes changes nothing. A server_proxy.conf that does not route to
# @backsoon was not written by backsoon and is never overwritten. NPM only reads the file when it
# reloads, which it does when a proxy host is saved or NPM restarts; this service cannot reload it.
set -eu

SRC=/usr/share/backsoon/server_proxy.conf
DIR=/npm-custom
DEST=$DIR/server_proxy.conf
CANONICAL='error_page 502 503 504 =503 @backsoon;'
NO_503='error_page 502 504 =503 @backsoon;'

log() { printf 'backsoon-npm-snippet: %s\n' "$*"; }
die() { log "$*" >&2; exit 1; }

if [ -z "${BACKSOON_NPM_CUSTOM_DIR:-}" ]; then
  log "BACKSOON_NPM_CUSTOM_DIR is not set: nothing installed"
  exit 0
fi
case ${BACKSOON_NPM_INTERCEPT_503:-true} in
  true) line=$CANONICAL ;;
  false) line=$NO_503 ;;
  *) die "BACKSOON_NPM_INTERCEPT_503 must be true or false" ;;
esac
[ -d "$DIR" ] || die "$DIR is not mounted: check BACKSOON_NPM_CUSTOM_DIR"
grep -qxF "$CANONICAL" "$SRC" || die "the built-in snippet lacks its error_page line"
if [ -e "$DEST" ] && ! grep -qF '@backsoon' "$DEST" 2>/dev/null; then
  die "$BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf exists and was not installed by backsoon: left untouched"
fi

# Not `test -w`: busybox reports every path writable for root, capabilities or not.
tmp=$(mktemp "$DIR/.server_proxy.conf.XXXXXX" 2>/dev/null) ||
  die "cannot write to $BACKSOON_NPM_CUSTOM_DIR (it is mounted read-only, or owned by another user: this service has no capabilities)"
trap 'rm -f "$tmp"' EXIT
while IFS= read -r l || [ -n "$l" ]; do
  if [ "$l" = "$CANONICAL" ]; then printf '%s\n' "$line"; else printf '%s\n' "$l"; fi
done < "$SRC" > "$tmp"
chmod 0644 "$tmp"
if [ -f "$DEST" ] && cmp -s "$tmp" "$DEST"; then
  log "$BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf unchanged"
  exit 0
fi
mv -f "$tmp" "$DEST"
trap - EXIT
log "installed $BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf; NPM applies it at its next reload (save any proxy host, or restart NPM)"
