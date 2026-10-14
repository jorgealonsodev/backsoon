#!/bin/sh
# backsoon-npm-snippet: installs the global NPM fallback (npm/server_proxy.conf, baked into the
# image) as server_proxy.conf in NPM's /data/nginx/custom directory, mounted here at /npm-custom.
#
# BACKSOON_NPM_CUSTOM_DIR    host path of that directory; unset means: install nothing, exit 0.
# BACKSOON_NPM_INTERCEPT_503 true (default) intercepts 502, 503 and 504; false leaves application
#                            503 responses alone (only 502 and 504).
# BACKSOON_NPM_RESTORE       true restores NPM's default configuration: removes backsoon's
#                            server_proxy.conf instead of installing it. Default false.
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

case ${BACKSOON_NPM_RESTORE:-false} in
  true) restore=1 ;;
  false) restore= ;;
  *) die "BACKSOON_NPM_RESTORE must be true or false" ;;
esac
if [ -z "${BACKSOON_NPM_CUSTOM_DIR:-}" ]; then
  if [ -n "$restore" ]; then
    log "BACKSOON_NPM_CUSTOM_DIR is not set: nothing removed; remove server_proxy.conf from NPM by hand, then reload NPM (docs/install.md)"
  else
    log "BACKSOON_NPM_CUSTOM_DIR is not set: nothing installed; install npm/server_proxy.conf in NPM by hand, then reload NPM (docs/install.md)"
  fi
  exit 0
fi

# backsoon only ever adds this one file and never overwrites a foreign one, so NPM's previous
# state is the file being absent: removing it is the whole restore.
if [ -n "$restore" ]; then
  [ -d "$DIR" ] || die "$DIR is not mounted: check BACKSOON_NPM_CUSTOM_DIR"
  if [ ! -e "$DEST" ]; then
    log "$BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf is not present: NPM already uses its default configuration"
    exit 0
  fi
  grep -qF '@backsoon' "$DEST" 2>/dev/null ||
    die "$BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf exists and was not installed by backsoon: left untouched"
  rm -f "$DEST" 2>/dev/null && [ ! -e "$DEST" ] ||
    die "cannot remove $BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf (the directory is mounted read-only, or owned by another user: this service has no capabilities)"
  log "removed $BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf: NPM is back to its default configuration"
  log "ACTION NEEDED: reload Nginx Proxy Manager so it drops the file: save any proxy host in NPM, restart the NPM container, or run: docker exec <npm> nginx -s reload; you can now remove the backsoon stack"
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
  log "$BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf unchanged; no reload needed"
  exit 0
fi
mv -f "$tmp" "$DEST"
trap - EXIT
log "installed $BACKSOON_NPM_CUSTOM_DIR/server_proxy.conf"
log "ACTION NEEDED: reload Nginx Proxy Manager so it applies the file: save any proxy host in NPM, restart the NPM container, or run: docker exec <npm> nginx -s reload"
