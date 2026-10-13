# Backsoon admin panel
Objective: build the backsoon-admin panel (PRD U4–U6) on top of the verified U0–U3 walking skeleton; UI designed with /impeccable. CI (U7) and production deploy (U8) stay out of this feature.
Branch: feat/backsoon-admin from local main fe6e9b2 (remote has no branches). Local commits only; no push/PR/merge.
Checks: go test ./... (admin), make lint, make test, make e2e (18080/18181 overrides); browser checks at 320/1280 light/dark.

## Specs
### S1 — Original request (verbatim)
> haz el panel con /impeccable

### S2 — Panel functional rules (verbatim PRD §5.2)
### 5.2 Panel de gestión (`backsoon-admin`)

| ID | Requisito | Prioridad | Criterio de aceptación | CA |
|---|---|---|---|---|
| RF-13 | **Acceso solo con usuario y contraseña.** Un único administrador definido por variables de entorno del stack de Portainer (§7.7). Ninguna vista ni endpoint del panel, salvo `/login` y `/healthz`, responde sin sesión válida. | Debe | Sin sesión, cualquier ruta redirige a `/login` (HTML) o devuelve 401 (peticiones `fetch`). | CA-17 |
| RF-14 | **Listado de páginas** (solo con sesión): todas las páginas con nombre, destinos asignados (dominios y servicio:puerto), fecha de última modificación, vista previa y acciones (editar, reemplazar HTML, borrar). La página por defecto aparece siempre y no se puede borrar. | Debe | Con 3 páginas creadas, el listado muestra las 3 más la página por defecto con sus destinos. | CA-18 |
| RF-15 | **Subir página:** subir un fichero `.html` (máx. 512 KB, UTF-8) o crearla desde la plantilla rellenando nombre de aplicación, mensaje, idioma e icono. | Debe | Ambas vías crean una página visible en el listado y servida por `backsoon`. | CA-19 |
| RF-16 | **Asignar destinos:** cada página tiene uno o más destinos. Tipos: dominio (`^[a-z0-9.-]+$`, se normaliza a minúsculas) o servicio:puerto (`^[a-z0-9.-]+:[0-9]{1,5}$`, puerto 1-65535). Un destino pertenece a una sola página: asignarlo a otra exige confirmación y lo quita de la anterior. | Debe | Destinos inválidos se rechazan con mensaje; un destino duplicado pide confirmación. | CA-20 |
| RF-17 | **Validación de contenido** al subir: rechazar si no es UTF-8 o supera el tamaño; **avisar** (y pedir confirmación) si contiene recursos externos (RF-11) o `<script>`. | Debe | Un HTML con `<link href="https://…">` muestra el aviso. | CA-14, CA-19 |
| RF-18 | **Vista previa segura:** la página se muestra en un `<iframe sandbox>` sin `allow-scripts` y con CSP restrictiva, para que un HTML subido no pueda ejecutar código en el origen del panel. | Debe | Un HTML con `<script>alert(1)</script>` no ejecuta nada en la vista previa. | CA-21 |
| RF-19 | **Editar y borrar:** cambiar destinos, reemplazar el HTML o borrar una página. Borrar libera sus destinos (pasan a la página por defecto). | Debe | Tras borrar, `curl` con ese destino sirve `default.html`. | CA-6 |
| RF-20 | **Publicación atómica:** el panel escribe los ficheros que lee `backsoon` con escritura a fichero temporal + `rename`, para que nunca se sirva una página a medio escribir. | Debe | Revisión de código + test unitario. | CA-22 |

> RF-9 (script `new-page.sh`) se elimina: lo sustituye el panel.

### S3 — Panel non-functional rules (verbatim PRD §6 rows)
| RNF-2 | **Huella:** `backsoon` < 10 MB de RAM en reposo; `backsoon-admin` < 50 MB de RAM y imagen < 30 MB. | `docker stats --no-stream`, `docker image ls`. | CA-15 |
| RNF-3 | **Disponibilidad:** ambos contenedores con `restart: unless-stopped` y `healthcheck` (`GET /healthz` → 200). Ninguno depende de las aplicaciones que protege. | Inspección del compose. | CA-8 |
| RNF-4 | **Degradación segura:** (a) si `backsoon-admin` está parado, `backsoon` sigue sirviendo páginas; (b) si `backsoon` está parado o no existe, NPM **arranca, recarga** y sirve todas las aplicaciones; solo se pierde la página amable (vuelve el 502 de NPM). Requiere `proxy_pass` por variable (resolución DNS en cada petición). | Los tres escenarios. | CA-5, CA-16 |
| RNF-8 | **Seguridad del panel:** contraseña guardada como hash bcrypt; sin credenciales configuradas, el panel **no arranca**; máximo 5 intentos fallidos por IP cada 15 min; cookie de sesión `HttpOnly`, `Secure`, `SameSite=Strict`, caducidad 12 h; token CSRF en toda petición que modifica; sin puertos publicados (se accede por NPM con HTTPS). | CA-17, CA-21, CA-24. | CA-17, CA-24 |
| RNF-9 | **Observabilidad:** `X-Backsoon: 1` en toda respuesta de mantenimiento; `backsoon` registra `host destino status fichero` (sin IP de cliente); el panel registra login correcto/fallido y cada alta/cambio/borrado. En NPM el fallback se identifica por `$upstream_status` `"502, 503"`. | `docker logs`. | CA-1 |
Scope note: only the panel (backsoon-admin) parts of these rows are new here; backsoon service parts were closed in the walking skeleton.

### S4 — Panel design (verbatim PRD §7.7)
### 7.7 Panel `backsoon-admin`

**Tecnología (decidida, D6):** binario Go con solo la biblioteca estándar (`net/http`, `html/template`, `embed`) más `golang.org/x/crypto/bcrypt`, en imagen `distroless/static`. Sin base de datos: metadatos en un fichero JSON. *Motivo:* imagen pequeña, un único binario y poca superficie de ataque.

**Variables de entorno (stack de Portainer):**

| Variable | Obligatoria | Descripción |
|---|---|---|
| `BACKSOON_ADMIN_USER` | Sí | Usuario administrador. |
| `BACKSOON_ADMIN_PASSWORD_HASH` | Sí (o la siguiente) | Hash bcrypt de la contraseña. Recomendado. |
| `BACKSOON_ADMIN_PASSWORD` | Alternativa | Contraseña en claro. Se acepta, pero el panel lo avisa en el log al arrancar. Si están las dos, gana el hash. |
| `BACKSOON_SESSION_SECRET` | Sí | Secreto aleatorio (≥ 32 bytes) para firmar sesiones. |
| `BACKSOON_REFRESH` | No (10) | Intervalo de recarga. Compartida con `backsoon`. |

Sin `BACKSOON_ADMIN_USER`, sin contraseña o sin `BACKSOON_SESSION_SECRET`, el panel termina con código ≠ 0 y un mensaje claro: nunca queda accesible sin login. Para cambiar la contraseña se cambia la variable y se redespliega.

**Vistas:**

| Ruta | Sesión | Contenido |
|---|---|---|
| `GET /login`, `POST /login` | No | Formulario de acceso. |
| `POST /logout` | Sí | Cierra la sesión. |
| `GET /` | Sí | **Listado de páginas** (RF-14): nombre, destinos, última modificación, vista previa, acciones. |
| `GET /pages/new`, `POST /pages` | Sí | Subir HTML o crear desde plantilla, y asignar destinos (RF-15, RF-16). |
| `GET /pages/{id}`, `POST /pages/{id}` | Sí | Editar destinos o reemplazar el HTML. |
| `POST /pages/{id}/delete` | Sí | Borrar (no disponible para la página por defecto). |
| `GET /pages/{id}/preview` | Sí | HTML servido con `Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:` para el iframe (RF-18). |
| `GET /healthz` | No | 200. |

**Modelo y publicación:**

- `backsoon_meta/sites.json` guarda las páginas: `{id, name, targets: [{type: host|target, value}], updated_at}`. El HTML original de cada página está en `backsoon_meta/pages/<id>.html`.
- Cada cambio **publica** en `backsoon_pages` (escritura temporal + `rename`, RF-20): `host/<dominio>.html` y `target/<servicio>_<puerto>.html` por cada destino de la página, y `default.html`. Los ficheros de destinos que ya no existen se borran.
- Al arrancar, el panel vuelve a publicar todo desde `sites.json` (así se recupera de cualquier inconsistencia) y crea `default.html` desde la plantilla si no existe.

### S5 — Networks and page template (verbatim PRD §7.5, §7.6)
### 7.5 Redes

- Se crea la red externa `proxy`, y **el contenedor de NPM debe estar conectado a ella** (cambio en el stack de NPM).
- `backsoon` y `backsoon-admin` se unen a `proxy` y no publican puertos.
- El panel se publica con un proxy host de NPM (p. ej. `backsoon.midominio.com` → `backsoon-admin:8080`) con certificado SSL. Se puede restringir además con una Access List de NPM.

### 7.6 Plantilla de página

Cada página es un HTML autocontenido. La plantilla (`templates/maintenance.html`, que el panel usa para "crear desde plantilla" y para generar `default.html`) cumple:

- **Autocontención:** CSS inline, fuentes del sistema, sin scripts. Imagen solo como emoji, SVG inline o `data:` URI. Objetivo < 10 KB.
- **Comportamiento:** `<meta http-equiv="refresh" content="{{REFRESH}}">` con el mismo valor que `Retry-After`; enlace visible "Reintentar ahora" (`<a href="">`); `<meta name="robots" content="noindex">`.
- **Accesibilidad y presentación:** `<html lang="{{LANG}}">` (por defecto `es`), `<title>`, un único `<h1>`, contraste AA, `<meta name="viewport">`, legible desde 320 px, modo oscuro con `prefers-color-scheme`, sin animaciones (o respetando `prefers-reduced-motion`).
- **Contenido:** nombre de la aplicación, icono opcional, mensaje "Volvemos en breve" más una línea explicativa, y aviso de que la página se actualiza sola.
- **Sin información interna:** ni códigos de error, ni versión de nginx, ni IP/puerto, ni rutas, ni la URI original. `default.html` no nombra ninguna aplicación ("Este servicio").
- **Variables:** `{{APP_NAME}}`, `{{MESSAGE}}`, `{{REFRESH}}`, `{{LANG}}`, `{{ICON}}`. El panel escapa en HTML los valores que introduce el usuario.

### S6 — Units (verbatim PRD §9)
| U4 | Panel, núcleo: autenticación con variables de entorno (RF-13, RNF-8), modelo `sites.json` y publicación atómica (RF-20), con tests unitarios. | U0 | `go test ./...` cubre: rechazo sin credenciales, bcrypt, rate limit, CSRF, validación de destinos, publicación y limpieza de ficheros. | `make test` → 0 |
| U5 | Panel, vistas: listado (RF-14), alta/subida/plantilla (RF-15), destinos (RF-16), validación (RF-17), vista previa (RF-18), edición y borrado (RF-19). | U4 | CA-17 … CA-22 pasan contra `compose.test.yml`. | `make e2e` → 0 |
| U6 | Integración panel ↔ `backsoon`: CA-6 (alta, cambio y borrado desde el panel, visibles sin reiniciar). | U3, U5 | `e2e.sh` crea una página por la API del panel, para `app1` y recibe esa página. | `make e2e` → 0 |

### S7 — Acceptance criteria (verbatim PRD §10)
| CA-6 | Desde el panel: crear página para `nueva.test`, modificarla y borrarla, sin reiniciar nada. | Cada cambio se ve en la siguiente petición; tras borrarla, `default.html`. |
| CA-7 | Redespliegue real desde Portainer de una aplicación que tarda > 10 s en arrancar. | El visitante ve mantenimiento todo el tiempo y entra en la aplicación al terminar (manual). |
| CA-14 | `grep -E '<script src\|<link \|@import\|url\(http'` sobre `templates/` y `default.html`; subir un HTML con `<link href="https://…">`. | Sin coincidencias; el panel muestra el aviso. |
| CA-16 | Con `backsoon` parado: `docker exec npm nginx -t`, `nginx -s reload` y `docker restart npm`. | Todo correcto; todas las aplicaciones responden. Con el panel parado, `backsoon` sigue sirviendo páginas. |
| CA-17 | Sin sesión: `GET /`, `GET /pages/new`, `POST /pages`. Arrancar el panel sin `BACKSOON_ADMIN_USER`. | Redirección a `/login` o 401; el panel no arranca (código ≠ 0). |
| CA-18 | Crear 3 páginas con destinos variados y abrir `/`. | Listado con las 3 + la página por defecto, sus destinos, fecha y vista previa. |
| CA-19 | Subir un `.html` válido; subir uno de 600 KB; subir uno que no es UTF-8; crear una desde la plantilla. | Alta correcta; rechazo; rechazo; alta correcta. |
| CA-20 | Destinos `APP.Test`, `app:99999`, `a b`, y un destino ya asignado. | Se normaliza a `app.test`; rechazo; rechazo; pide confirmación y lo mueve. |
| CA-21 | Vista previa de un HTML con `<script>alert(1)</script>`; `POST` sin token CSRF. | No se ejecuta nada; 403. |
| CA-22 | Test unitario de publicación con un fallo a mitad de escritura. | Nunca queda un fichero parcial en `backsoon_pages`. |
| CA-24 | 6 logins fallidos seguidos desde la misma IP. | El sexto se rechaza aunque la contraseña sea correcta, hasta pasados 15 min. |
Scope note: CA-7 (real Portainer redeploy) is U8 and manual; not claimed here.

### S8 — Explicit user decisions
- Panel UI language: “Inglés”.
- Audience: “Open source para otros”.
- Devices: “Escritorio y móvil por igual”.
- Design start: “Primero imagen de referencia” (comp-first), then — no image generation available in this environment — “Directamente en código” (code-first; no external Stitch).
- main: “Integrar U0–U3 en main” → local main created at fe6e9b2, no push.

## Tasks
- T1 | S1,S8 | inline: /impeccable init (PRODUCT.md) + new-work direction round (code-led) and direction contract; browser screenshots instead of comps | done | commit: see L6
- T2 | S2–S4,S6(U4),S7(CA-17,CA-20,CA-22,CA-24) | done (own checks + independent verify PASS): worker mv75udhl-d-4cls | commit 522105cb6eaa6f9e115edb194f9af1eb64d4fd7e | delegated worker in parallel with T1 (disjoint: admin/internal/{config,auth,pages,publish}): env config, bcrypt, sessions, CSRF, rate limit, sites.json model, target validation, atomic publish + cleanup, go tests | pending | commit: pending
- T3 | S2–S5,S6(U5),S7(CA-14,CA-17–CA-22) | delegated: web mv76aciw/mv775tic/mv77fg3h + packaging/e2e mv776p92; parent browser review and fixes | done | commits 6f5eedbd34203db5d691a87bb6869b0d9b66f3e2, 63909deeb245aaed07a29ea7ab3f823c69538117; independent verify mv77y638-j-e3ic PASS
- T4 | S2,S6(U6),S7(CA-6,CA-16) | e2e inside T3 (63909de); inline docs README + env.example | done | commits 2bd920ffa5502bc2d423cc1f6bd55c0fa86f1115 + closure docs commit (L13)

## Log
L1 — User request: “haz el panel con /impeccable”. Preceded by “si” to “¿Quieres que prepare la integración en main o que empiece con el panel de administración?”, clarified as “Integrar U0–U3 en main”; local main created at fe6e9b2 (fast-forward pointer, remote empty, no push).
L2 — impeccable context: NO_PRODUCT_MD / PRODUCT_INIT_REQUIRED; no visual implementation for the panel. Init interview answered: English UI, open source for others, desktop and mobile equally, comp-first.
L3 — Large task (several sessions, separate deliverables). Branch feat/backsoon-admin created from main. Go 1.26.9 available locally; admin/go.mod declares module github.com/jorgealonsodev/backsoon/admin, go 1.26.
L4 — PRODUCT.md and .impeccable/config.json written. No image models available (models.getAvailableOfType("image") = [], impeccable reported no IMAGE_GEN). User chose “Directamente en código” over Google Stitch; buildPath set to code. T2 core worker mv75udhl-d-4cls launched in background (allowed: admin/internal/{config,auth,pages,publish}/**, admin/go.mod, admin/go.sum).
L5 — Surface questions: main task “Comprobar qué página ve un destino”; volume “Pocas (1–10)”; avoid “Parecer un SaaS genérico”. concept-seed efa48271 (operate) assigned grounded candidate 4 of 7 (lobby directory board); decision page answer: {"optionId":"assigned","buildPath":"code"}. Direction contract recorded in .impeccable/surfaces/admin-internal-web.md (target admin/internal/web). DESIGN.md is owed at the finish review (T3), not before.
L6 — T1 committed 1dea53a4c304f2eea302b86b78ecec110e707079. T2 worker mv75udhl-d-4cls completed: packages config, auth, pages, publish, publish/atomic; x/crypto v0.58.0 (go directive 1.26 → 1.26.0, required by x/crypto). Worker RED: undefined symbols in every package before implementation. Parent spot check: gofmt -l empty, go vet exit 0, go test -count=1 ./... all 5 packages ok.
Decisions recorded from worker report: limiter records a failure up front and Success clears it; a rejected attempt does not extend the block; 4096-key bound. Moving a page's last destination with confirmation leaves it with zero destinations (still listed and editable); the UI must show that state and warn in the conflict confirmation (RF-16 'uno o más destinos' is enforced on Create/Update input). sites.json is {"sites":[…]}; corrupt meta fails Open instead of overwriting. Client IP for the limiter (T3): X-Real-IP set by NPM, else RemoteAddr; panel has no published ports. bcrypt '$' escaping in compose/Portainer: README note in T4.
L7 — T2 committed 522105cb6eaa6f9e115edb194f9af1eb64d4fd7e. ASSESS over 1dea53a..522105c committedOnly: unassessable (untracked declaration), writer large → high path; independent verifier mv769fl4-e-vlca launched (read-only, scratch confined to .odd-checks/verify-admin-core.*). T3 split: web worker mv76aciw-f-wwgn (admin/internal/web/**, admin/cmd/backsoon-admin/**) now; packaging (Dockerfile, compose, Makefile, lint, .dockerignore) and admin e2e follow once the binary exists. Design decision: RF-13 allows only /login and /healthz without session, so CSS is inlined and fonts are served only to authenticated sessions; the login page uses the system fallback stack. Fonts Archivo and Martian Mono (OFL, licences beside the files) subset to Latin-1 woff2 under admin/internal/web/static/fonts.
L8 — Independent verifier mv769fl4-e-vlca: T2 PASS, no blockers. Race tests on the four core package trees exit 0 (listed packages instead of ./... to avoid compiling the concurrent web worker's code). Probes: config errors name variables without values; REFRESH Go vs shell identical on 19 edge values; cookie Path=/ Max-Age=43200 HttpOnly Secure SameSite=Strict; Verify rejects tampering/expiry/other secret; limiter 6th rejected until +15m, 200 goroutines → exactly 5 admitted; 200k-string fuzz: every accepted target matches nginx maps and published filename; 524288 bytes accepted / 524289 rejected; single-pass escaping; injected mid-write failure leaves old content and no temp; cleanup removes only stale *.html; corrupt sites.json unchanged; traversal ids → ErrNotFound.
Advisories (not auto-fixed): (1) empty REFRESH → 10 in Go vs exit 1 in shell (unreachable via compose/image); (2) `app.test.` / `app..x` accepted but never served by nginx; `app:080` → `app:80`; (3) pages root 0700 under umask 077; (4) ensureDir chmods root-owned host/ and target/ → EPERM for a non-root panel — MUST be handled in T3 packaging (volume ownership); (5) failed publish leaves a mix of old/new complete files until next publish; (6) stateless sessions not revocable on logout; 4096-key eviction depends on client-IP derivation; (7) names from sites.json not re-validated.
Confinement: scratch under .odd-checks/verify-admin-core.30kpUP; Go t.TempDir() dirs under /tmp/user/1000 were created and auto-removed (none remain). Core paths unchanged before/after.
L9 — Web worker mv76aciw-f-wwgn delivered the web layer + binary (RED 67 failing tests on a 404 stub; parent: 7 packages ok). Parent browser review (Chromium, 1280/320, light/dark) found: preview breaks when a link inside it is clicked (opaque-origin navigation drops the SameSite=Strict cookie → 303 /login → frame-ancestors 'none'); meta refresh is blocked by the sandbox; destinations break mid-token at 320 px; /favicon.ico 401/404. Continuation mv775tic-g-df9o fixed preview (401 framed notice under sandbox CSP, RED 3 cases 303≠401) and favicon (data: URI); parent spot check web+cmd ok.
User: “no me gusta mucho el diseño vamos a elegir otro”; dislikes “Los colores”; “Se siente retro el diseño tiene que ser actual”. Round 2 (re-roll 1) presented the gallery-label direction + transit-lines pick + flat-colour map; user re-rolled again. Asked for reference products: “Portainer / Nginx Proxy Manager” → canon (standing exit). Reference captured from a disposable jc21/nginx-proxy-manager:2.16.0 container (Tabler UI, system-ui font, gray-50 ground, white cards with coloured top line, uppercase table headers, mono chips, status pills), container removed. Surface brief contract and PRODUCT.md brand commitment updated. Redesign delegated to mv77fg3h-i-ach1 (visual only; routes, statuses, headers, field names unchanged; self-hosted fonts removed). Packaging+e2e worker mv776p92-h-g6ku told to assert only on stable contracts.
L10 — Redesign (mv77fg3h-i-ach1, partial: could not delete files) delivered the NPM/Portainer-style visual layer; parent removed the now-unused self-hosted fonts (untracked files the parent had downloaded) and fontface.css. Parent checks: gofmt empty, go vet 0, go test -count=1 ./... 7 packages ok. Browser review round 1 (Chromium; login, empty, home 1280/390/320 light+dark, lookup hit/miss, inline preview, actions menu, edit, new 320 dark, warning confirm, move confirm): no horizontal overflow anywhere; console errors only the expected sandbox/CSP blocks (meta refresh, <script>, external stylesheet) and the 409 conflict page — CA-21 behaviour confirmed in a real browser. Findings: lookup icon overlapped the text (generic input rule overrode padding-left, same specificity) and the lookup field was cramped at 320 px. Parent fixed panel.css inline (higher-specificity selector; wrap below 40rem). Round 2: padding 36px vs icon right edge 29px at 1280 and 320, no overflow. impeccable detect on templates+CSS: []. DESIGN.md written from the built tokens. Incident: a parent `pgrep -f` cleanup matched its own shell and terminated that command (exit 143); replaced by exact /proc/<pid>/exe matching; an earlier review server survived job_stop because `timeout` runs the child in its own process group — killed by exact executable path.
L11 — Web interface committed 6f5eedbd34203db5d691a87bb6869b0d9b66f3e2. Packaging+e2e worker mv776p92-h-g6ku completed: admin/Dockerfile (golang:1.26.9-alpine and distroless static-debian12:nonroot pinned by index digest; image 19.4 MB; RAM 8.2 MiB), backsoon-init/pages-init `seed && chown -R 65532:65532 /pages` (negative control: old seeding → panel exits 1 `chmod /pages/host: operation not permitted`), panel hardening read_only/cap_drop ALL/no-new-privileges/tmpfs, lint parity check prod vs test init, harness panel mode BACKSOON_TEST_WITH_ADMIN=1 and SO_REUSEADDR port probe, NPM reload after stopping app1/panel (stale per-worker IP). Worker results: make lint 36 ok; make test unit 248/248 + go 7 ok; admin-e2e 388/0; make e2e 111/0 (RNF-5 A 458 µs / B 456 µs, −0.33%, run once) + admin 388/0; no leftover resources. Parent diff review: no blockers; T4 README must explain bcrypt `$` escaping for compose/Portainer. Parent spot check `make lint` running (job-20).
L12 — Parent spot checks after packaging: make lint 36 ok / 0 FAIL; packaging committed 63909deeb245aaed07a29ea7ab3f823c69538117. ASSESS f99a148..63909de committedOnly: unassessable (untracked declaration) → independent verifier mv77y638-j-e3ic launched (T3 web + packaging + admin e2e, one admin-e2e run, no RNF-5 rerun).
T4 docs inline (passive; no meaningful RED). Evidence gathered first with Docker Compose 5.6.0 and a printenv probe container: a bcrypt hash is intact from the process environment, from .env with single quotes, or with every $ doubled; .env unquoted or double-quoted truncates it to `$2y$10` (compose warns about an unset variable). The panel then refuses to start (`BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`), so the failure is loud. Portainer docs do not state how UI variables are interpolated; README gives the verified forms and the error-driven `$$` fix for Portainer without claiming its mechanism. `htpasswd -nbB` defaults to cost 5 ($2y$05$); README recommends `-nbBC 12` (verified: $2y$12$, 60 chars, accepted by config.Load through a temporary in-package test that existed for seconds under admin/internal/config while the verifier was running read-only — removed immediately; worktree clean). httpd:2.4-alpine image pulled for the check and removed. env.example now documents every variable as active with an empty single-quoted hash; compose config with env.example exit 0 (init/backsoon get only REFRESH; panel gets the five). make lint passed after the docs change; git diff --check clean.
L13 — Independent verifier mv77y638-j-e3ic: T3 PASS, no blockers. Own runs: make lint 36 ok/0 FAIL exit 0; go test -count=1 -race ./... 7/7 ok (temporary zz_dochash file not compiled: fresh GOCACHE had no entry for it); admin-e2e 388 passed/0 failed exit 0 (project backsoon-verify-t3-0a4bcf); image 19,382,104 bytes; RAM 8.6 MiB; Docker inventory identical before/after (9/44/10/51). Evidence per item: session gate before routing (server.go:111-127), 303/401 split, framed 401 notice; exits 1 without credentials (4 image starts); CSRF on logout/delete/create/update (403 + unchanged listing); login nonce cookie Path=/login; 6th login 429 Retry-After 900; cookies Secure/HttpOnly/SameSite=Strict, Max-Age 12 h; X-Real-IP really used (ip=172.26.0.1 gateway); preview CSP exact; iframes sandbox with no tokens; panel CSP nonce, script-src 'none', frame-ancestors 'none'; drafts bounded/TTL/single-use/410 cross-session; RNF-9 log lines without secrets; packaging hardening, RNF-4a, seed unchanged, ownership 65532 755/644; DESIGN.md claims match CSS.
Advisories (not auto-fixed): (1) X-Real-IP trusted from any peer → limiter bypass from inside the proxy network; CDN shared-IP lockout — documented in README; (2) weaker e2e assertions: CA-18 date count, client-address check only rejects loopback, two-step move only unit-tested, no forged-X-Real-IP e2e; (3) no log line for the page that loses moved destinations; (4) stateless sessions not revoked at logout; (5) oversized/unreadable submit returns the form before the CSRF check (nothing changes); (6) test backsoon does not wait for pages-init; (7) BusyBox chown -R symlink behaviour, no practical impact. Not verified by the verifier: real browser (parent L10) and the 15-minute unblock in e2e (unit-tested in T2). Verifier scratch .odd-checks/verify-admin-t3.OvmlGQ (gitignored).
Native review: ASSESS over every unit (522105c, f99a148..63909de) failed read-only with the untracked-declaration error; no reviewDue was offered and the user did not request a native review of this feature, so none was started (RDD on, not disabled). README status updated; T3 and T4 closed on observed proof. No push, PR or merge to main.
