# backsoon: static nginx that serves maintenance pages from the /pages volume (no extra runtime).
# Build context is the repository root so the page template has a single source of truth.
# alpine-slim is enough: its entrypoint ships envsubst (checked with `which envsubst`).
FROM nginx:1.30.5-alpine-slim@sha256:32463212baf0e7d91aded2e9b843a4f2b9e017804b8c9d5bae7b51dcef64389c

# Only ${BACKSOON_REFRESH} is substituted into the templates; nginx variables such as ${bs_svc}
# are left alone because the entrypoint substitutes just the variables matched by this filter.
ENV BACKSOON_REFRESH=10 \
    NGINX_ENVSUBST_FILTER='^BACKSOON_REFRESH$'

RUN rm -f /etc/nginx/conf.d/default.conf
COPY --chmod=0644 nginx/nginx.conf /etc/nginx/nginx.conf
COPY --chmod=0644 nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --chmod=0755 nginx/docker-entrypoint.d/ /docker-entrypoint.d/
COPY --chmod=0644 templates/maintenance.html /usr/share/backsoon/maintenance.html
