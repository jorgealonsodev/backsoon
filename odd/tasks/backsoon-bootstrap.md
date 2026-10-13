# Backsoon walking skeleton
Objective: begin PRD development with U0–U3; admin U4–U7 and production U8 are outside this first feature.
Branch: feat/backsoon-bootstrap (from local unborn main; remote default not probed). Delivery: auto-chain / feature-branch-chain, local work-unit slices only; no push/PR/merge.
Checks: make lint, make test, make e2e; isolated Docker projects. Forecast: 900–1400 authored lines excluding the existing PRD; split by deliverable, never shrink tests for a line budget.

## Specs
### S1 — Original authorization
> '/mnt/480GB/Proyectos/Personales/20261013_backsoon/20261013_backsoon_git/backsoon/PRD.md' comienza el desarrollo con odd

### S2 — Maintenance functional rules (verbatim PRD §5.1)
### 5.1 Servicio de mantenimiento (NPM + `backsoon`)

| ID | Requisito | Prioridad | Criterio de aceptación | CA |
|---|---|---|---|---|
| RF-1 | Para cualquier URI y **cualquier método HTTP**, si el upstream devuelve 502/503/504 o no es alcanzable (conexión rechazada → 502; timeout → 504), NPM sirve la página de mantenimiento. | Debe | `curl -X GET` y `curl -X POST` a una ruta cualquiera con la aplicación parada devuelven 503 y el HTML de mantenimiento. | CA-1, CA-9 |
| RF-2 | La página se elige en este orden: (1) **dominio** = `$host` (minúsculas, sin puerto); (2) **servicio:puerto** = destino del proxy host (`$server:$port` de NPM); (3) página por defecto. | Debe | Con páginas para dominio y para servicio:puerto, se sirve la del dominio; sin ella, la del servicio:puerto; sin ninguna, `default.html`. | CA-1, CA-3, CA-10 |
| RF-3 | Si ningún destino coincide se sirve `default.html`. Si tampoco existe, 503 con cuerpo vacío (nunca 404). | Debe | Como se describe. | CA-3 |
| RF-4 | La página se recarga sola cada `BACKSOON_REFRESH` segundos (por defecto 10) con `<meta http-equiv="refresh">`, y ofrece un enlace "Reintentar ahora". | Debe | El HTML generado contiene `content="<BACKSOON_REFRESH>"`. | CA-2, CA-11 |
| RF-5 | Respuesta con código **503** y `Retry-After: <BACKSOON_REFRESH>`, aunque el upstream devolviera 502 o 504. | Debe | Cabecera presente exactamente una vez; código 503. | CA-1 |
| RF-6 | `Cache-Control: no-store` y `X-Backsoon: 1` en toda respuesta de mantenimiento. Solo las añade `backsoon`; `server_proxy.conf` no añade cabeceras. | Debe | Cada cabecera aparece exactamente una vez. | CA-1 |
| RF-7 | La configuración en NPM es un único archivo `/data/nginx/custom/server_proxy.conf`; ningún proxy host necesita configuración avanzada propia. | Debe | Revisión + CA-4. | CA-4, CA-8 |
| RF-8 | Crear, modificar o borrar una página (desde el panel) surte efecto en la siguiente petición, sin reiniciar NPM ni `backsoon`. | Debe | Las tres operaciones se verifican con `curl`. | CA-6 |
| RF-10 | Los códigos interceptados son la única línea `error_page … @backsoon;` de `server_proxy.conf`. El README documenta la variante `502 504` (para aplicaciones que devuelven 503 legítimamente). | Debería | Con `error_page 502 504`, una aplicación que responde 503 muestra su propio 503. | CA-13 |
| RF-11 | Las páginas son autocontenidas: sin `<script src`, `<link`, `@import` ni `url(http…)`. Imágenes solo como SVG inline o `data:` URI. | Debe | El panel lo valida (RF-17); `grep` en CA-14. | CA-14 |
| RF-12 | Si la petición tiene `Accept` que empieza por `application/json`, se devuelve `{"status":"maintenance","retry_after":<N>}` con `Content-Type: application/json` y 503. El resto (incluido `*/*`) recibe HTML. | Debería | `curl -H "Accept: application/json"` devuelve JSON y 503. | CA-9 |

### S3 — Service non-functional rules (verbatim PRD)
| RNF-1 | **Simplicidad del servicio de páginas:** `backsoon` es un nginx estático (`nginx:alpine-slim`; si su entrypoint no incluye `envsubst`, `nginx:alpine`) con `worker_processes 1`. Sin runtime adicional. | `docker exec backsoon ps` muestra solo nginx. | CA-15 |
| RNF-2 | **Huella:** `backsoon` < 10 MB de RAM en reposo; `backsoon-admin` < 50 MB de RAM y imagen < 30 MB. | `docker stats --no-stream`, `docker image ls`. | CA-15 |
| RNF-3 | **Disponibilidad:** ambos contenedores con `restart: unless-stopped` y `healthcheck` (`GET /healthz` → 200). Ninguno depende de las aplicaciones que protege. | Inspección del compose. | CA-8 |
| RNF-4 | **Degradación segura:** (a) si `backsoon-admin` está parado, `backsoon` sigue sirviendo páginas; (b) si `backsoon` está parado o no existe, NPM **arranca, recarga** y sirve todas las aplicaciones; solo se pierde la página amable (vuelve el 502 de NPM). Requiere `proxy_pass` por variable (resolución DNS en cada petición). | Los tres escenarios. | CA-5, CA-16 |
| RNF-5 | **Rendimiento:** coste cero en peticiones correctas; `@backsoon` solo se ejecuta tras un error. | 200 peticiones a una aplicación viva con y sin `server_proxy.conf`: mediana con desvío ≤ 5 % (Supuesto). | CA-4 |
| RNF-6 | **Portabilidad:** un único stack de Portainer; las páginas viven en volúmenes con nombre (sobreviven a "pull and redeploy"); instalación en 4 pasos (§13). | Seguir el README en limpio. | CA-23 |
| RNF-7 | **Seguridad del servicio de páginas:** sin puertos publicados; `server_tokens off`; `autoindex off`; `$host` y el destino validados por `map` antes de usarlos en rutas; nunca se refleja contenido de la petición en la respuesta. | `docker port backsoon` vacío + curls de CA-10. | CA-10 |
| RNF-9 | **Observabilidad:** `X-Backsoon: 1` en toda respuesta de mantenimiento; `backsoon` registra `host destino status fichero` (sin IP de cliente); el panel registra login correcto/fallido y cada alta/cambio/borrado. En NPM el fallback se identifica por `$upstream_status` `"502, 503"`. | `docker logs`. | CA-1 |
| RNF-10 | **Reversibilidad:** desactivar = renombrar `server_proxy.conf` y recargar NPM; sin migraciones. | Runbook §13. | CA-25 |
Scope note: admin measurements and admin behavior within these rows are deferred to U4–U7, not claimed by this walking skeleton.

### S4 — Service reference design (verbatim PRD §7.4; requirements outrank sample code)
### 7.4 Configuración del contenedor `backsoon`

Archivo: `nginx/default.conf.template` (procesado por `envsubst` del entrypoint oficial de nginx; solo sustituye `${BACKSOON_REFRESH}`).

```nginx
# Destino recibido de NPM, validado: "192.168.1.10:8081" -> "192.168.1.10_8081"
map $http_x_backsoon_target $bs_target {
    "~^(?<svc>[a-z0-9.-]+):(?<prt>[0-9]{1,5})$"  "${svc}_${prt}";
    default                                      "none";
}

# RF-12: JSON solo si se pide explícitamente
map $http_accept $bs_format {
    "~^application/json"  json;
    default               html;
}

server {
    listen 80 default_server;
    server_name _;
    server_tokens off;
    root /pages;

    location = /healthz { access_log off; return 200 "ok\n"; }

    # Toda petición es mantenimiento: 503 + redirección interna (convierte el método en GET)
    location / { return 503; }
    error_page 503 /__backsoon;

    location = /__backsoon {
        internal;
        add_header Cache-Control "no-store" always;
        add_header Retry-After ${BACKSOON_REFRESH} always;
        add_header X-Backsoon 1 always;
        if ($bs_format = json) { rewrite ^ /__backsoon.json last; }
        try_files /host/$host.html /target/$bs_target.html /default.html =503;
    }

    location = /__backsoon.json {
        internal;
        default_type application/json;
        add_header Cache-Control "no-store" always;
        add_header Retry-After ${BACKSOON_REFRESH} always;
        add_header X-Backsoon 1 always;
        return 503 '{"status":"maintenance","retry_after":${BACKSOON_REFRESH}}';
    }
}
```

Notas:
- `$host` ya llega validado por nginx (minúsculas, sin `/` ni `..`), así que `try_files /host/$host.html` no permite salir de `/pages`.
- `add_header … always` es obligatorio: sin `always`, nginx no añade cabeceras a respuestas 503.
- `open_file_cache` sigue desactivado (es lo que trae nginx por defecto), así que los cambios del panel se ven en la siguiente petición (RF-8).
- El entrypoint oficial solo sustituye variables de entorno **definidas**, así que `${svc}` y `${prt}` del `map` no se tocan. No definir variables con esos nombres.
- *Validar en U2*: que `if` + `rewrite` dentro de `/__backsoon` respeta el 503, y que `alpine-slim` incluye `envsubst`.

### S5 — Page template (verbatim PRD §7.6)
### 7.6 Plantilla de página

Cada página es un HTML autocontenido. La plantilla (`templates/maintenance.html`, que el panel usa para "crear desde plantilla" y para generar `default.html`) cumple:

- **Autocontención:** CSS inline, fuentes del sistema, sin scripts. Imagen solo como emoji, SVG inline o `data:` URI. Objetivo < 10 KB.
- **Comportamiento:** `<meta http-equiv="refresh" content="{{REFRESH}}">` con el mismo valor que `Retry-After`; enlace visible "Reintentar ahora" (`<a href="">`); `<meta name="robots" content="noindex">`.
- **Accesibilidad y presentación:** `<html lang="{{LANG}}">` (por defecto `es`), `<title>`, un único `<h1>`, contraste AA, `<meta name="viewport">`, legible desde 320 px, modo oscuro con `prefers-color-scheme`, sin animaciones (o respetando `prefers-reduced-motion`).
- **Contenido:** nombre de la aplicación, icono opcional, mensaje "Volvemos en breve" más una línea explicativa, y aviso de que la página se actualiza sola.
- **Sin información interna:** ni códigos de error, ni versión de nginx, ni IP/puerto, ni rutas, ni la URI original. `default.html` no nombra ninguna aplicación ("Este servicio").
- **Variables:** `{{APP_NAME}}`, `{{MESSAGE}}`, `{{REFRESH}}`, `{{LANG}}`, `{{ICON}}`. El panel escapa en HTML los valores que introduce el usuario.

### S6 — Foundation U0/U2 (verbatim PRD)
| U0 | Andamiaje: estructura de §8, `Makefile`, `LICENSE`, `admin/go.mod`. | — | `make lint` ejecuta `docker compose config` y `nginx -t` (con `server_proxy.conf` envuelto en un `server{}` de prueba). | `make lint` → 0 |
| U2 | Núcleo de `backsoon`: `default.conf.template`, `default.html` provisional, servicio en `docker-compose.yml`. | U0 | `test/unit.sh` comprueba: 503, `Retry-After`, `Cache-Control`, `X-Backsoon`, orden host → servicio:puerto → default, `Host` malicioso, POST → 503, JSON con `Accept`. | `make test` → 0 |
Structure reference: PRD.md §8. Only walking-skeleton paths are implemented; no fake admin handlers or CI success.

### S7 — Local NPM harness U1 (verbatim PRD)
| U1 | Arnés local: `compose.test.yml` (`jc21/nginx-proxy-manager`, `backsoon`, `traefik/whoami` como `app1`), `test/npm-bootstrap.sh` (login en la API y proxy host `app1.test` → `app1:80`). | U0 | `curl -H 'Host: app1.test' localhost:8080` devuelve la salida de whoami. | `make e2e-up && curl …` |
**Arnés local (`compose.test.yml`):** una red interna; NPM expone solo `8080:80` y `8181:81`; `app1` es `traefik/whoami`. `e2e.sh` usa `docker compose stop/start app1` para simular redespliegues y `stop backsoon` para CA-5 y CA-16. No se prueba TLS, porque no afecta a `error_page`.

### S8 — NPM integration U3 (verbatim PRD)
| U3 | **Versión mínima de extremo a extremo:** `server_proxy.conf` (§7.3). | U1, U2 | `e2e.sh` pasa CA-1, CA-3, CA-4, CA-5 y CA-16 (NPM recarga y arranca con `backsoon` parado). Comprueba que `$server:$port` llega a `backsoon`. Si algo falla, se corrige el diseño aquí. | `make e2e` → 0 |
### 7.3 Configuración global en NPM

Archivo: `npm/server_proxy.conf` → copiar a `/data/nginx/custom/server_proxy.conf`. NPM lo incluye al final de cada bloque `server` de proxy host, pero **no vigila el directorio**: tras copiarlo hay que validar y recargar.

```nginx
# backsoon — NPM incluye este archivo al final de cada bloque server de proxy host.
# Tras copiarlo: docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
proxy_intercept_errors on;
error_page 502 503 504 =503 @backsoon;   # RF-10: edita esta lista (p. ej. "502 504")

location @backsoon {
    set $backsoon_upstream http://backsoon:80;    # variable => DNS resuelto en cada petición
    proxy_pass $backsoon_upstream;                # usa resolvers.conf de NPM (valid=10s)
    proxy_intercept_errors off;                   # no re-interceptar el 503 de backsoon
    proxy_set_header Host $host;
    proxy_set_header X-Backsoon-Target "$server:$port";   # destino del proxy host (variables de NPM)
}
```

Por qué cada línea (verificado contra la plantilla `proxy_host.conf` de NPM y el código de nginx):

- **`=503`**: sin él, nginx conserva el código original (502/504) y no se cumple RF-5.
- **`proxy_pass` por variable**: con un nombre fijo, nginx lo resuelve al cargar la configuración. Si `backsoon` no existe en ese momento, `nginx -t` falla y **caen todos los proxy hosts**; además, la IP queda fijada hasta el siguiente reload.
- **`proxy_intercept_errors off`** dentro de `@backsoon`: si se hereda `on`, el 503 de `backsoon` se vuelve a interceptar y nginx sirve su página 503 por defecto. La página de `backsoon` no llegaría nunca al cliente.
- **Sin `add_header`**: NPM reenvía las cabeceras que pone `backsoon`, que así son la única fuente.
- **`$server` y `$port`**: NPM los define en cada proxy host para su propio `proxy_pass`. *Validar en U3* que están disponibles dentro de `@backsoon`.
- Si `backsoon` está caído, el `proxy_pass` de `@backsoon` falla, `recursive_error_pages` está `off` y NPM sirve su 502 por defecto (RNF-4).

### S9 — Acceptance criteria (verbatim PRD)
| CA-1 | Parar `app1`; `curl -si -X GET` a una ruta cualquiera de su dominio. | 503; cuerpo de la página de `app1`; `Retry-After`, `Cache-Control: no-store` y `X-Backsoon: 1`, cada una exactamente una vez. |
| CA-2 | Arrancar `app1` de nuevo. | En ≤ `BACKSOON_REFRESH` + 2 s tras responder la aplicación, el navegador la muestra (manual). |
| CA-3 | Dominio sin página propia y destino sin página, con la aplicación parada. | `default.html` con 503. |
| CA-4 | Todas las aplicaciones funcionando. | Respuestas idénticas a las de sin `server_proxy.conf`; el log de `backsoon` no tiene líneas nuevas. |
| CA-5 | Parar `backsoon` y parar `app1`. | 502 por defecto de NPM en `app1`; el resto de aplicaciones responde. |
| CA-8 | `docker compose config`; `nginx -t` en `backsoon` y en NPM. | Sin errores. |
| CA-9 | Con `app1` parada: `curl -X POST …` y `curl -H "Accept: application/json" …`. | 503 + HTML (nunca 405); 503 + JSON respectivamente. |
| CA-10 | Contra `backsoon`: `Host: APP1.test:8443`; `Host: ../etc/passwd`; `X-Backsoon-Target: ../x:1`; página de dominio y de servicio:puerto a la vez. | Página de `app1.test`; 400 o default; default; gana la del dominio. Nunca contenido fuera de `/pages`. |
| CA-11 | Arrancar con `BACKSOON_REFRESH=5` y crear una página desde la plantilla. | `content="5"` y `Retry-After: 5`. |
| CA-13 | Cambiar a `error_page 502 504 …`; aplicación que responde 503. | Se ve el 503 propio de la aplicación (manual). |
| CA-14 | `grep -E '<script src\|<link \|@import\|url\(http'` sobre `templates/` y `default.html`; subir un HTML con `<link href="https://…">`. | Sin coincidencias; el panel muestra el aviso. |
| CA-15 | `docker stats --no-stream`, `docker image ls`, `docker exec backsoon ps`. | Dentro de los límites de RNF-1 y RNF-2. |
| CA-16 | Con `backsoon` parado: `docker exec npm nginx -t`, `nginx -s reload` y `docker restart npm`. | Todo correcto; todas las aplicaciones responden. Con el panel parado, `backsoon` sigue sirviendo páginas. |
Manual browser/production checks are not represented as automated proof. The panel part of CA-14/CA-16 is deferred.

### S10 — Explicit user decisions
- License: “MIT”.
- Local branches/commits: “Sí, rama y commits locales”.
- Delivery: “Cadena sobre rama de funcionalidad — recomendada”.
No authorization for publishing, merging, production deployment, or review consent.

## Tasks
- T0 | S1,S10 | inline: initialize requirement baseline and ODD continuity; passive docs, no writer trigger | done | commit: 4495d711c6e368c4cbef0fd479558749c58b6dbe; git diff --cached --check passed
- T1 | S2–S6,S9,S10 | delegated: static service plus foundation (U0/U2); parallel unit disjoint from T2 and heavier than agent startup | in_progress | complete report recovered: RED setup failure (no curl assertions), GREEN make test 231/231, lint PASS with NPM snippet deferred; parent spot check/independent verification/commit pending
- T2 | S7,S10 | delegated: real NPM harness (U1); parallel unit disjoint from T1 and heavier than agent startup | done | self-tests, parent structural check and independent verifier PASS; commit: c1e2b080a01f4c1e9df45939cde8b0d297d5f4ae; native review unavailable then explicitly left unreviewed by user, no authority created
- T3 | S2,S3,S8,S9 | inline: integrate NPM fallback (U3), full seam checks, independent verification and risk-gated native review | pending | commit: pending

## Log
### L1 — Original request
'/mnt/480GB/Proyectos/Personales/20261013_backsoon/20261013_backsoon_git/backsoon/PRD.md' comienza el desarrollo con odd

### L2 — User continuation and decisions
“Review the delivered subagent output and continue.”
1. License — MIT
2. Branch/commits — Sí, rama y commits locales
3. Delivery — Cadena sobre rama de funcionalidad — recomendada

### L3 — Exploration and boundaries
Read-only explorer mv6qm8ss-1-ad72 mapped PRD.md. First scope is U0–U3, explicitly prioritized by §9; implementation of the panel and production operations remains later work, not open tasks in this feature. No product decision blocks the skeleton after license selection. Go stdlib plus bcrypt is already decided in §7.7.
Docker daemon 29.9.0, Compose v5.6.0, Go 1.26.9 are available. Repository has no commits; existing untracked .claude/ and CLAUDE.md remain untouched. Remote publication/default verification is not authorized; use local feature branch and defer actual remote chain setup.
Official nginx docs confirm error_page to URI changes non-GET/HEAD methods to GET; named locations preserve method. See https://nginx.org/en/docs/http/ngx_http_core_module.html.
The nginx reference sample is not proof: validate headers, missing-default empty body, host/target validation, envsubst filtering, JSON, and NPM variables with real images.
Review mode is on globally. After self-checks assess candidate risk; high/unassessable gets independent verification before native review. Consent is separate from commits. Slices: baseline → T1 service → T2 harness → T3 integration. No code-golf to fit 400 lines.

### L4 — T2 progress and isolated port collision
T2 worker reports pinned official jc21/nginx-proxy-manager:2.16.0 and traefik/whoami:v1.12.0, with initial-user API setup discovered from the image. This is progress, not completed U1 proof; parent will verify its final delivery. Parent ss confirms 127.0.0.1:8080 is occupied; 18080/18181 are free at this point. Preserve harness defaults while all local checks use BACKSOON_TEST_HTTP_PORT=18080 BACKSOON_TEST_ADMIN_PORT=18181. No unrelated container/network/data changes are authorized. T2 was steered accordingly. Scratch whoami health is probed through NPM, not shell execution inside whoami.

### L5 — T1 progress and service/harness seam
T1 reports official nginx:1.30.5-alpine-slim pulled with envsubst present and digest sha256:32463212baf0e7d91aded2e9b843a4f2b9e017804b8c9d5bae7b51dcef64389c. Public curl tests are being written before RED and implementation; no completion or GREEN evidence yet. Provisional default will be seeded by a one-shot backsoon-init service with the same image, preserving existing files; runtime backsoon remains a read-only consumer. Parent relayed the image/seeding contract to T2 and requested an isolated missing-default test unaffected by reseeding.

### L6 — T2 delivered, verification gate still open
Worker mv6rkn6f-3-tcd7 settled completed: U1 works with real NPM 2.16.0 and whoami 1.12.0 on 18080/18181; RED absent compose/script/refused curl, GREEN harness up plus both whoami hosts, bootstrap idempotency and drift correction; isolated project teardown passed. Parent ran compose config, shell syntax and git diff --check with exit 0. Actual T1 nginx not yet integrated. Native ASSESS could not assess ambient untracked files (explicit declaration required), so risk is unassessable and verification follows high; committed T2 range will isolate the candidate from T1's ongoing edits. T2 is not marked done before independent proof. Worker reported a leftover scratch directory outside repo after blocked cleanup; parent will not retry external deletion. Existing unrelated 8080 listener untouched. T2 slice is about 520 authored lines, a cohesive deploy/bootstrap/check unit; retain tests/docs rather than code-golf for advisory 400-line budget.

### L7 — Independent T2 proof
Verifier mv6rut1c-4-6ofy settled: S7 PASS and delivered S10 PASS, no blockers. Isolated git archive c1e2b08 in repository scratch .odd-checks/u1.IvhU6t; backsoon-verify-u1 at 28080/28181. Compose validation, separate shell parses, up, both public 200 whoami bodies, repeated bootstrap unchanged, check, guarded invalid-input probes and down passed. All project-specific resources removed; unrelated 8080 listener unchanged. Remaining local scratch/log files are not implementation artifacts and will be ignored; no deletion retry after child safety rejection. Literal make e2e-up and actual T1 profile remain T3 proof, not claimed here.
Advisories (not blockers, no automatic correction): urllib redirects may forward test bearer token; loopback alone does not establish test NPM identity; two inner curls have no per-attempt timeout; documented sh -n a b parses only a; edge bridge is additional to PRD internal network; range/wait validation is partial. Parent preserves these follow-ups without treating advisories as authorized expanded fixes. Native review assesses only c1e2b08 against baseline 4495d71, not T1's active worktree.

### L8 — Review preflight deferred until parallel writer settles
Native START for committed U1 range was refused in preflight: invalid_request, untracked inventory changed; mutation_outcome not_started, no lineage or review authority created. T1 is still adding files, so do not race another start or replay stale bindings. Wait for its completion, refresh INSPECT with explicit untracked exclusion, then use a fresh committed-only START for the U1 commit before advancing HEAD. Independent U1 proof remains valid. This is not consent decline or approval and does not authorize delivery.

### L9 — Reconcile actual work and review blocker
T1 implementation worker mv6rjuar-2-eyoz settled; latest result is only an ignore-file addendum, reporting make test 231/231 PASS and make lint PASS with NPM snippet skipped. The full original RED/GREEN/interface report was not delivered to parent, so read-only recovery mv6s37y2-5-ftza is running; no T1 writer remains active. T1 not done: parent spot check, risk-gated independent verification, commit and applicable review remain unobserved.
With writers settled, fresh INSPECT excluded untracked files and offered START; committed-only START was refused before mutation with native-start-retained-selection-candidate-mismatch, next_action inspect-and-resolve-the-current-intended-untracked-selection. No lineage or authority was created. Stop automatic review attempts; T2 remains independently verified but closure awaits user choice to leave this exact candidate unreviewed or resolve native tooling. No reset/recovery or switch changes authorized. T3 remains pending behind T1/T2, not implemented or tested. Todo labels were corrected to distinguish completed implementation from verified closure.

### L10 — Explicit candidate disposition and recovered T1 evidence
User selected: “Continuar sin revisión nativa de c1e2b08” (leave-c1e2b08-unreviewed). T2 closes on its functional and independent verification plus local work-unit commit; RDD remains enabled, no grant or disposition is inferred for another candidate.
T1 report recovered by read-only task mv6s37y2-5-ftza. RED sh test/unit.sh exit2 was missing nginx/Dockerfile setup, not public failing curl behavior; four diagnostic runs followed before GREEN, ending with make test 231 passed/0 failed exit0. Lint exit0 skipped only actual NPM snippet until T3. No behavior-RED claim is made. Approx943 authored lines with tests retained. Default seeding preservation tested; serving uses custom build nginx/Dockerfile with pinned official1.30.5-alpine-slim digest32463212…; nginx.conf worker1 and ENV NGINX_ENVSUBST_FILTER required by T3 (T2 stock image is not production-equivalent). RF8 live updates, RAM, browser320px/contrast and malformed-request log privacy were not proven by worker; defer admin aspects but verify applicable service behavior. Full test log was written outside project by worker; parent will not access/delete it without consent, and own logs stay in project scratch. .odd-checks is ignored.
