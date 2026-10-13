# backsoon-admin: the management panel as one static Go binary on distroless (no shell, no libc).
# Build context is the repository root (like nginx/Dockerfile) so the page template keeps a single
# source of truth: docker build -f admin/Dockerfile .
# Both base images are pinned by the digest of their multi-arch index.
FROM golang:1.26.9-alpine@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS build

WORKDIR /src/admin
COPY admin/go.mod admin/go.sum ./
RUN go mod download
COPY admin/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backsoon-admin ./cmd/backsoon-admin
# distroless has no shell, so the directories are staged here with their final owners and modes.
# /meta and /pages belong to 65532: a fresh named volume copies that owner on first use. The
# template tree stays root-owned and world-readable (COPY --chmod would also strip the x bit
# from the directories it creates implicitly).
COPY templates/maintenance.html /skel/root/usr/share/backsoon/maintenance.html
RUN mkdir -p /skel/own/meta /skel/own/pages \
 && chmod 0755 /skel/root/usr /skel/root/usr/share /skel/root/usr/share/backsoon \
 && chmod 0644 /skel/root/usr/share/backsoon/maintenance.html

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/backsoon-admin /usr/local/bin/backsoon-admin
COPY --from=build /skel/root/ /
COPY --from=build --chown=65532:65532 /skel/own/ /

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/backsoon-admin"]
