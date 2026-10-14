#!/bin/sh
# First message of every backsoon start: the NPM fallback only takes effect once NPM reloads.
echo "$0: reminder: after installing or updating backsoon, reload Nginx Proxy Manager so it applies server_proxy.conf: save any proxy host in NPM, restart the NPM container, or run: docker exec <npm> nginx -s reload" | sed 's#^/docker-entrypoint.d/##'
