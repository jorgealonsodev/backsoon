# Backsoon U7: CI and installation guide
Objective: PRD U7 — CI workflow and a README installation guide that a clean install can follow (CA-23). U8 (production) is out of scope: it operates the owner's real servers.
Branch: feat/backsoon-ci from local main 91c2af9. Local commits only; no push/PR/merge. "CI en verde" can only be observed on GitHub after a push, which is not authorized.
Checks: make lint, make test; workflow validated locally (actionlint); CA-23 rehearsal in an isolated project and network.

## Specs
### S1 — Request (verbatim)
> lo que consideres mas optimo
(answering: merge to local main, X-Real-IP limiter advisory, next block CI (U7) or production (U8))

### S2 — Unit (verbatim PRD §9)
| U7 | Documentación y CI: README (prerrequisitos, NPM unido a `proxy`, instalación §13, verificación, generar el hash bcrypt) y `ci.yml`. | U3, U6 | Instalación desde cero siguiendo el README (CA-23). | CI en verde |

### S3 — Acceptance (verbatim PRD §10)
| CA-23 | Instalación desde cero siguiendo el README en un host limpio. | 4 pasos; CA-1 pasa (manual). |

[66 more lines in file. Use offset=375 to continue.]

### S4 — Runbook (verbatim PRD §13)
## 13. Runbook

**Instalación (4 pasos)**

1. `docker network create proxy` (si no existe) y conectar el contenedor de NPM a `proxy` (en su stack de Portainer).
2. Desplegar el stack `backsoon` desde Portainer, con las variables de §7.7. El hash se genera con `docker run --rm httpd:alpine htpasswd -nbB admin '<contraseña>' | cut -d: -f2`. Comprobar: `docker exec backsoon wget -qSO- http://localhost/ 2>&1 | grep -E '503|X-Backsoon'`.
3. Copiar `npm/server_proxy.conf` a `<volumen_npm>/nginx/custom/server_proxy.conf.new`, renombrarlo a `server_proxy.conf` y ejecutar `docker exec <npm> nginx -t && docker exec <npm> nginx -s reload`.
4. Crear en NPM el proxy host del panel (`backsoon.midominio.com` → `backsoon-admin:8080`, con SSL), entrar y asignar las páginas a las aplicaciones.

**Verificación**

- `curl -sI https://app1.midominio.com` con la aplicación parada → `503`, `Retry-After`, `Cache-Control: no-store`, `X-Backsoon: 1`.
- `curl -sI -X POST …` → 503 (no 405). `curl -sI -H 'Accept: application/json' …` → `application/json`.
- `docker restart <npm>` con `backsoon` parado → todas las aplicaciones responden.
- Ver cuántas páginas de mantenimiento se sirvieron: `grep '"502, 503"' /data/logs/proxy-host-*_access.log`.

**Rollback**

`mv server_proxy.conf server_proxy.conf.off && docker exec <npm> nginx -s reload`. NPM incluye el fichero de forma opcional, así que la recarga no falla. Los contenedores `backsoon` pueden seguir encendidos o pararse sin efecto.

**Tras actualizar NPM**

`docker exec <npm> nginx -t`, repetir CA-1 y CA-16 (o `make e2e` con la nueva versión de la imagen en `compose.test.yml`).

[1 more lines in file. Use offset=440 to continue.]

### S5 — Repository structure (PRD §8, CI line verbatim)
└── .github/workflows/ci.yml      # lint + test + go test en cada push; e2e manual

### S6 — Parent decisions (delegated by the user)
- Local main fast-forwarded to 91c2af9 (no push).
- X-Real-IP limiter advisory: kept documented, not changed (exploiting it requires a container already inside the proxy network; alternatives create admin lockout or mandatory config).
- The host already has a Docker network named `proxy` (owner's real NPM): never touch it; rehearsals use an isolated network/project name.
- Known PRD runbook correction: NPM 2.16 logs the controlled fallback as `502 : 503`, not `"502, 503"` (observed in U3).

## Tasks
- T1 | S2,S5 | delegated worker mv78h7rb-k-fuxr (.github/**) | done locally (GitHub run unobserved: push not authorized) | commit: see L2
- T2 | S2–S4,S6 | delegated worker (disjoint: README.md, docs/install*): installation guide (4 steps), verification, rollback, after-NPM-update; CA-23 rehearsal in an isolated project/network | in_progress | commit: pending

## Log
L1 — User: “lo que consideres mas optimo”. Parent decisions recorded in S6. Feature doc created; two parallel workers (disjoint surfaces).
L2 — T1 worker mv78h7rb-k-fuxr delivered .github/workflows/ci.yml: push/pull_request/workflow_dispatch; lint and test jobs (setup-go from admin/go.mod) run make lint / make test; e2e only on workflow_dispatch runs make e2e and uploads .odd-checks/ on failure (include-hidden-files); permissions contents: read; concurrency cancel-in-progress; persist-credentials false; timeouts 20/20/45. Parent checks: actionlint v1.7.12 exit 0 with no findings; checkout v7.0.1 3d3c42e5…, setup-go v7.0.0 b7ad1dad…, upload-artifact v7.0.2 cf430e03… each re-resolved with git ls-remote and matching. Unverified until a GitHub run: runner Docker compose/buildx features used by the scripts, setup-go fetching Go 1.26.0, Docker Hub anonymous pull limits, timeouts, and the RNF-5 5% latency gate on noisy shared runners (manual e2e only).
