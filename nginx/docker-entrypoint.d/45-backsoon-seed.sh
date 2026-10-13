#!/bin/sh
# Seed the pages volume with the provisional default.html and the host/ and target/ directories.
#
# Never overwrites: if default.html already exists (seeded earlier or created from the panel) the
# volume is left exactly as it is. Production runs this once from the backsoon-init service, which
# mounts the volume read-write; in the read-only serving container it is a no-op or a notice.
set -eu

ME=$(basename "$0")
PAGES=/pages
TEMPLATE=/usr/share/backsoon/maintenance.html

sh /docker-entrypoint.d/05-backsoon-validate.sh

[ ! -e "$PAGES/default.html" ] || exit 0

umask 022
tmp="$PAGES/.default.html.$$"
trap 'rm -f "$tmp"' EXIT

# `test -w` is true for root even on a read-only mount, so probe with a real write (`true`, not
# `:`: a failed redirection on a special builtin would terminate the shell).
if ! { true >"$tmp"; } 2>/dev/null; then
    echo "$ME: notice: $PAGES is read-only and has no default.html; run the backsoon-init service to seed it"
    exit 0
fi

mkdir -p "$PAGES/host" "$PAGES/target"
sed -e 's|{{APP_NAME}}|Este servicio|g' \
    -e 's|{{MESSAGE}}|Estamos realizando tareas de mantenimiento. Esta página volverá a mostrar el servicio en cuanto esté disponible.|g' \
    -e "s|{{REFRESH}}|$BACKSOON_REFRESH|g" \
    -e 's|{{LANG}}|es|g' \
    -e 's|{{ICON}}|🔧|g' \
    "$TEMPLATE" >"$tmp"

# A hard link fails if default.html appeared meanwhile, so a concurrent seed cannot overwrite it.
if ln "$tmp" "$PAGES/default.html" 2>/dev/null; then
    echo "$ME: seeded $PAGES/default.html (refresh ${BACKSOON_REFRESH}s)"
fi
