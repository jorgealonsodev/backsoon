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
# Every init entrypoint is PID 1, so $$ is not unique across containers sharing
# this volume. Create a private inode on the same filesystem before rendering.
# This also probes write access: test -w is unreliable for root on a ro mount.
if ! tmp=$(mktemp "$PAGES/.default.html.XXXXXX" 2>/dev/null); then
    echo "$ME: notice: $PAGES is read-only and has no default.html; run the backsoon-init service to seed it"
    exit 0
fi
trap 'rm -f "$tmp"' EXIT

mkdir -p "$PAGES/host" "$PAGES/target"
sed -e 's|{{APP_NAME}}|Este servicio|g' \
    -e 's|{{MESSAGE}}|Estamos realizando tareas de mantenimiento. Esta página volverá a mostrar el servicio en cuanto esté disponible.|g' \
    -e "s|{{REFRESH}}|$BACKSOON_REFRESH|g" \
    -e 's|{{LANG}}|es|g' \
    -e 's|{{ICON}}|🔧|g' \
    "$TEMPLATE" >"$tmp"

# mktemp starts at 0600; nginx's unprivileged worker must be able to read the
# complete file before it becomes the public default (the serving volume is ro).
chmod 0644 "$tmp"

# Publish the complete private inode without replacing an existing default.
# Losing seeders only remove their own temporary file, never the winner's inode.
if ln "$tmp" "$PAGES/default.html" 2>/dev/null; then
    echo "$ME: seeded $PAGES/default.html (refresh ${BACKSOON_REFRESH}s)"
fi
