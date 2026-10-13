# PRD — backsoon

**Página de mantenimiento automática por aplicación para Nginx Proxy Manager, con panel de gestión**

| Campo | Valor |
|---|---|
| Nombre del repositorio | `backsoon` |
| Fecha | 2026-10-13 |
| Estado | Borrador v2 (revisado: requisitos, arquitectura, plan, riesgos y alcance) |
| Entorno objetivo | Docker + Portainer + Nginx Proxy Manager (NPM) |

---

## 0. Glosario

| Término | Significado |
|---|---|
| `backsoon` | Contenedor nginx que sirve las páginas de mantenimiento. Solo lee ficheros. |
| `backsoon-admin` | Contenedor con el panel web para gestionar páginas. Separado de `backsoon`. |
| NPM | Nginx Proxy Manager, el reverse proxy. |
| proxy host | Entrada de dominio en NPM que apunta a un destino. |
| aplicación | Servicio protegido por `backsoon`. |
| destino | Lo que identifica a una aplicación para elegir su página: un **dominio** (`app1.midominio.com`) o un **servicio:puerto** (`192.168.1.10:8081`, `app1:3000`), tal como está configurado en el proxy host de NPM. |
| no responde | Cualquier causa que hace que NPM obtenga 502/503/504 del upstream. |
| redespliegue | Recreación del contenedor de una aplicación desde Portainer. |
| página de mantenimiento | HTML autocontenido servido por `backsoon`. |
| página por defecto | `default.html`; se sirve cuando ningún destino coincide. |
| desviar | Salto interno de nginx (`error_page`) hacia `backsoon`. **No** es una redirección HTTP: la URL del navegador no cambia. |
| intervalo de recarga | Valor único `BACKSOON_REFRESH` (segundos), usado en `Retry-After` y en el `<meta refresh>`. |

## 1. Resumen

`backsoon` muestra una página de mantenimiento propia de cada aplicación cuando ésta no responde (por ejemplo, durante un redespliegue desde Portainer). Cuando la aplicación vuelve, el tráfico llega a ella de nuevo sin intervención manual ni cambios de configuración.

Se apoya en el comportamiento nativo de nginx: si el upstream falla, NPM obtiene un 502/503/504. Una **única configuración global** en NPM intercepta esos errores y los **desvía internamente** a `backsoon`, que elige la página según el **dominio** solicitado o el **servicio:puerto** al que apunta el proxy host, y si no hay ninguna, sirve la página por defecto. La respuesta es un **503 con `Retry-After`**, no cacheable.

Las páginas se gestionan desde un **panel web** (`backsoon-admin`) protegido por un único usuario administrador definido en las variables de entorno del stack de Portainer. El panel permite subir páginas, asignarles destinos y ver todas las páginas existentes.

## 2. Contexto y situación actual

- Las aplicaciones se despliegan como stacks en **Portainer**; cada una **expone un puerto en el host**.
- Un contenedor de **NPM** actúa como reverse proxy: cada dominio tiene un proxy host que apunta a `IP_HOST:PUERTO` (o a `contenedor:puerto`).
- Durante un redespliegue el contenedor se recrea; mientras, el puerto no responde y NPM devuelve su error genérico (502 Bad Gateway).

**Problema:** el visitante ve un error técnico, sin marca de la aplicación y sin indicación de que el servicio volverá en breve. Además, hoy no hay forma cómoda de gestionar páginas por aplicación.

## 3. Objetivos y no-objetivos

### Objetivos (v1)

| ID | Objetivo | Cubierto por |
|---|---|---|
| O-1 | Mostrar una página de mantenimiento por aplicación cuando no responde; página por defecto si no tiene una propia. | RF-1, RF-2, RF-3 |
| O-2 | Volver a servir la aplicación automáticamente cuando responda. | RF-4, CA-2, CA-7 |
| O-3 | Configuración **centralizada** en NPM: un solo archivo para todos los proxy hosts; añadir una aplicación no toca NPM. | RF-7, RF-8 |
| O-4 | Gestionar las páginas (subir, asignar a dominio o servicio:puerto, listar, borrar) desde un panel web con login. | RF-13 … RF-20 |
| O-5 | El servicio de páginas es mínimo (nginx estático) y **no depende** del panel: si el panel cae, las páginas se siguen sirviendo. | RNF-1, RNF-4 |
| O-6 | Desplegable como un único stack de Portainer. | RNF-6 |

### No-objetivos (v1)

- Mantenimiento *planificado* (bloquear tráfico con la aplicación viva). Ver §12 "Futuro".
- Varios usuarios, roles, registro o recuperación de contraseña en el panel.
- Subir páginas con recursos separados (ZIP con CSS/imágenes). En v1 la página es un único HTML autocontenido.
- Leer los proxy hosts desde la API de NPM para autocompletar destinos.
- Monitorización, alertas o métricas de disponibilidad.
- Negociación de idioma (`Accept-Language`); el idioma se fija por página.
- Soporte para proxies distintos de NPM.

## 4. Usuarios y casos de uso

| Usuario | Caso de uso |
|---|---|
| Administrador | Redespliega una aplicación desde Portainer; los visitantes ven su página de mantenimiento en lugar de un 502. |
| Administrador | Entra en el panel con usuario y contraseña, sube un HTML (o lo crea desde la plantilla) y lo asigna a uno o varios destinos: dominios y/o servicio:puerto. Sin reiniciar nada. |
| Administrador | Entra en el panel y ve el listado de todas las páginas, con sus destinos y una vista previa. |
| Visitante | Entra en el dominio durante un redespliegue, ve "Volvemos en breve" y la página se recarga sola hasta que la aplicación responde. |
| Cliente no navegador (API, formulario POST) | Recibe 503 durante el redespliegue (nunca 405); si pide `Accept: application/json`, recibe un JSON. |

## 5. Requisitos funcionales

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

## 6. Requisitos no funcionales

| ID | Requisito | Medición | CA |
|---|---|---|---|
| RNF-1 | **Simplicidad del servicio de páginas:** `backsoon` es un nginx estático (`nginx:alpine-slim`; si su entrypoint no incluye `envsubst`, `nginx:alpine`) con `worker_processes 1`. Sin runtime adicional. | `docker exec backsoon ps` muestra solo nginx. | CA-15 |
| RNF-2 | **Huella:** `backsoon` < 10 MB de RAM en reposo; `backsoon-admin` < 50 MB de RAM y imagen < 30 MB. | `docker stats --no-stream`, `docker image ls`. | CA-15 |
| RNF-3 | **Disponibilidad:** ambos contenedores con `restart: unless-stopped` y `healthcheck` (`GET /healthz` → 200). Ninguno depende de las aplicaciones que protege. | Inspección del compose. | CA-8 |
| RNF-4 | **Degradación segura:** (a) si `backsoon-admin` está parado, `backsoon` sigue sirviendo páginas; (b) si `backsoon` está parado o no existe, NPM **arranca, recarga** y sirve todas las aplicaciones; solo se pierde la página amable (vuelve el 502 de NPM). Requiere `proxy_pass` por variable (resolución DNS en cada petición). | Los tres escenarios. | CA-5, CA-16 |
| RNF-5 | **Rendimiento:** coste cero en peticiones correctas; `@backsoon` solo se ejecuta tras un error. | 200 peticiones a una aplicación viva con y sin `server_proxy.conf`: mediana con desvío ≤ 5 % (Supuesto). | CA-4 |
| RNF-6 | **Portabilidad:** un único stack de Portainer; las páginas viven en volúmenes con nombre (sobreviven a "pull and redeploy"); instalación en 4 pasos (§13). | Seguir el README en limpio. | CA-23 |
| RNF-7 | **Seguridad del servicio de páginas:** sin puertos publicados; `server_tokens off`; `autoindex off`; `$host` y el destino validados por `map` antes de usarlos en rutas; nunca se refleja contenido de la petición en la respuesta. | `docker port backsoon` vacío + curls de CA-10. | CA-10 |
| RNF-8 | **Seguridad del panel:** contraseña guardada como hash bcrypt; sin credenciales configuradas, el panel **no arranca**; máximo 5 intentos fallidos por IP cada 15 min; cookie de sesión `HttpOnly`, `Secure`, `SameSite=Strict`, caducidad 12 h; token CSRF en toda petición que modifica; sin puertos publicados (se accede por NPM con HTTPS). | CA-17, CA-21, CA-24. | CA-17, CA-24 |
| RNF-9 | **Observabilidad:** `X-Backsoon: 1` en toda respuesta de mantenimiento; `backsoon` registra `host destino status fichero` (sin IP de cliente); el panel registra login correcto/fallido y cada alta/cambio/borrado. En NPM el fallback se identifica por `$upstream_status` `"502, 503"`. | `docker logs`. | CA-1 |
| RNF-10 | **Reversibilidad:** desactivar = renombrar `server_proxy.conf` y recargar NPM; sin migraciones. | Runbook §13. | CA-25 |

## 7. Diseño de la solución

### 7.1 Arquitectura

```
                  ┌───────────────────────────────────────────────┐
  Internet ─────▶ │ Nginx Proxy Manager                           │
                  │  server_proxy.conf (global):                  │
                  │   error_page 502 503 504 =503 @backsoon;      │
                  └──┬───────────────┬────────────────┬───────────┘
                     │ OK            │ 502/503/504    │ proxy host del panel
                     ▼               ▼                ▼ (backsoon.midominio.com)
              ┌────────────┐  ┌──────────────────┐  ┌───────────────────────┐
              │ Aplicación │  │ backsoon (nginx) │  │ backsoon-admin (panel)│
              │ IP:puerto  │  │ lee /pages (ro)  │  │ login · listado · CRUD│
              └────────────┘  └────────▲─────────┘  └──────────┬────────────┘
                                       │   volumen backsoon_pages (rw)      │
                                       └────────────────────────────────────┘
```

- `backsoon` y `backsoon-admin` comparten el volumen `backsoon_pages`: el panel escribe y `backsoon` solo lee.
- Los metadatos del panel (qué página tiene qué destinos) viven en otro volumen, `backsoon_meta`, que `backsoon` no monta.
- Ningún contenedor publica puertos. NPM llega a ambos por la red Docker `proxy`.

### 7.2 Flujo de una petición

1. El visitante solicita `https://app1.midominio.com/ruta`.
2. NPM hace `proxy_pass` al destino de la aplicación (`$server:$port`).
3. **Caso normal:** la aplicación responde y NPM devuelve la respuesta. `backsoon` no interviene.
4. **Caso caída:** la conexión es rechazada (502) o expira (504, tras los timeouts de NPM, 60 s por defecto), o la aplicación responde 503. `proxy_intercept_errors` + `error_page … =503 @backsoon` hacen que NPM salte a `@backsoon`.
5. `@backsoon` hace `proxy_pass` a `backsoon` (resuelto en cada petición por el DNS de Docker), con `proxy_intercept_errors off`, y le envía `Host` y `X-Backsoon-Target: $server:$port`.
6. `backsoon` responde `return 503`. Por `error_page 503 /__backsoon` (redirección interna a URI, que convierte el método en GET) sirve, por este orden, `/pages/host/<host>.html`, `/pages/target/<servicio>_<puerto>.html` o `/pages/default.html`, con 503, `Retry-After`, `Cache-Control: no-store` y `X-Backsoon: 1`.
7. NPM entrega el 503 al cliente. El navegador recarga la página cada `BACKSOON_REFRESH` s; cuando la aplicación responde, se cumple el paso 3.

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

## 8. Estructura del repositorio y entregables

```
backsoon/
├── README.md                     # Instalación, verificación, uso del panel, tras actualizar NPM
├── LICENSE
├── Makefile                      # lint | test | e2e | e2e-up | e2e-down
├── docker-compose.yml            # Stack de producción (Portainer)
├── compose.test.yml              # Stack local: NPM + backsoon + backsoon-admin + app dummy (whoami)
├── nginx/
│   └── default.conf.template     # Configuración de backsoon (§7.4)
├── npm/
│   └── server_proxy.conf         # Para /data/nginx/custom/ de NPM (§7.3)
├── templates/
│   └── maintenance.html          # Plantilla de página (§7.6)
├── admin/                        # Panel backsoon-admin (§7.7)
│   ├── Dockerfile
│   ├── go.mod
│   ├── cmd/backsoon-admin/main.go
│   └── internal/                 # auth, pages (dominio), publish (ficheros), web (handlers + templates)
├── test/
│   ├── unit.sh                   # curl directo a backsoon: 503, cabeceras, orden de selección, traversal, JSON
│   ├── npm-bootstrap.sh          # Crea el proxy host de prueba por la API de NPM
│   └── e2e.sh                    # CA-1…CA-6, CA-9, CA-16 contra compose.test.yml
└── .github/workflows/ci.yml      # lint + test + go test en cada push; e2e manual
```

**Entregables v1:** el contenedor `backsoon` funcional; `server_proxy.conf` validado contra la imagen oficial de NPM; el panel `backsoon-admin`; el arnés `make test` / `make e2e` reproducible sin tocar producción; README con instalación, verificación y procedimiento tras actualizar NPM.

## 9. Plan de implementación

**Principio:** unidades verticales pequeñas, cada una verificable con un comando. **Primero, la versión mínima de extremo a extremo (U1 + U2 + U3)**: una petición que atraviesa un NPM real → `@backsoon` → `backsoon` y vuelve con 503 y la página correcta, y un NPM que recarga sin errores con `backsoon` ausente. Así se validan el primer día los supuestos de mayor riesgo (§7.3), en local y sin tocar producción.

| ID | Unidad | Deps | Definition of Done | Verificación |
|---|---|---|---|---|
| U0 | Andamiaje: estructura de §8, `Makefile`, `LICENSE`, `admin/go.mod`. | — | `make lint` ejecuta `docker compose config` y `nginx -t` (con `server_proxy.conf` envuelto en un `server{}` de prueba). | `make lint` → 0 |
| U1 | Arnés local: `compose.test.yml` (`jc21/nginx-proxy-manager`, `backsoon`, `traefik/whoami` como `app1`), `test/npm-bootstrap.sh` (login en la API y proxy host `app1.test` → `app1:80`). | U0 | `curl -H 'Host: app1.test' localhost:8080` devuelve la salida de whoami. | `make e2e-up && curl …` |
| U2 | Núcleo de `backsoon`: `default.conf.template`, `default.html` provisional, servicio en `docker-compose.yml`. | U0 | `test/unit.sh` comprueba: 503, `Retry-After`, `Cache-Control`, `X-Backsoon`, orden host → servicio:puerto → default, `Host` malicioso, POST → 503, JSON con `Accept`. | `make test` → 0 |
| U3 | **Versión mínima de extremo a extremo:** `server_proxy.conf` (§7.3). | U1, U2 | `e2e.sh` pasa CA-1, CA-3, CA-4, CA-5 y CA-16 (NPM recarga y arranca con `backsoon` parado). Comprueba que `$server:$port` llega a `backsoon`. Si algo falla, se corrige el diseño aquí. | `make e2e` → 0 |
| U4 | Panel, núcleo: autenticación con variables de entorno (RF-13, RNF-8), modelo `sites.json` y publicación atómica (RF-20), con tests unitarios. | U0 | `go test ./...` cubre: rechazo sin credenciales, bcrypt, rate limit, CSRF, validación de destinos, publicación y limpieza de ficheros. | `make test` → 0 |
| U5 | Panel, vistas: listado (RF-14), alta/subida/plantilla (RF-15), destinos (RF-16), validación (RF-17), vista previa (RF-18), edición y borrado (RF-19). | U4 | CA-17 … CA-22 pasan contra `compose.test.yml`. | `make e2e` → 0 |
| U6 | Integración panel ↔ `backsoon`: CA-6 (alta, cambio y borrado desde el panel, visibles sin reiniciar). | U3, U5 | `e2e.sh` crea una página por la API del panel, para `app1` y recibe esa página. | `make e2e` → 0 |
| U7 | Documentación y CI: README (prerrequisitos, NPM unido a `proxy`, instalación §13, verificación, generar el hash bcrypt) y `ci.yml`. | U3, U6 | Instalación desde cero siguiendo el README (CA-23). | CI en verde |
| U8 | Despliegue en producción (operación, no desarrollo): runbook §13 con aplicaciones reales. | U7 | CA-1, CA-2 y CA-7 con una aplicación real; anotar en el README la versión de NPM validada. | Checklist §13 |

**Paralelización:** U1 ∥ U2 ∥ U4 (ficheros distintos). Después de U3 y U4, U5. U6 une ambas ramas; U7 cierra. Camino crítico: U0 → U1/U2 → U3 → U6 → U7.

**Arnés local (`compose.test.yml`):** una red interna; NPM expone solo `8080:80` y `8181:81`; `app1` es `traefik/whoami`. `e2e.sh` usa `docker compose stop/start app1` para simular redespliegues y `stop backsoon` para CA-5 y CA-16. No se prueba TLS, porque no afecta a `error_page`.

## 10. Criterios de aceptación y pruebas

Se automatizan en `test/unit.sh` (contra `backsoon` directamente, con `curl -H Host:`), `test/e2e.sh` (stack local con NPM) y `go test` (panel), salvo donde se indica "manual".

| ID | Prueba | Resultado esperado |
|---|---|---|
| CA-1 | Parar `app1`; `curl -si -X GET` a una ruta cualquiera de su dominio. | 503; cuerpo de la página de `app1`; `Retry-After`, `Cache-Control: no-store` y `X-Backsoon: 1`, cada una exactamente una vez. |
| CA-2 | Arrancar `app1` de nuevo. | En ≤ `BACKSOON_REFRESH` + 2 s tras responder la aplicación, el navegador la muestra (manual). |
| CA-3 | Dominio sin página propia y destino sin página, con la aplicación parada. | `default.html` con 503. |
| CA-4 | Todas las aplicaciones funcionando. | Respuestas idénticas a las de sin `server_proxy.conf`; el log de `backsoon` no tiene líneas nuevas. |
| CA-5 | Parar `backsoon` y parar `app1`. | 502 por defecto de NPM en `app1`; el resto de aplicaciones responde. |
| CA-6 | Desde el panel: crear página para `nueva.test`, modificarla y borrarla, sin reiniciar nada. | Cada cambio se ve en la siguiente petición; tras borrarla, `default.html`. |
| CA-7 | Redespliegue real desde Portainer de una aplicación que tarda > 10 s en arrancar. | El visitante ve mantenimiento todo el tiempo y entra en la aplicación al terminar (manual). |
| CA-8 | `docker compose config`; `nginx -t` en `backsoon` y en NPM. | Sin errores. |
| CA-9 | Con `app1` parada: `curl -X POST …` y `curl -H "Accept: application/json" …`. | 503 + HTML (nunca 405); 503 + JSON respectivamente. |
| CA-10 | Contra `backsoon`: `Host: APP1.test:8443`; `Host: ../etc/passwd`; `X-Backsoon-Target: ../x:1`; página de dominio y de servicio:puerto a la vez. | Página de `app1.test`; 400 o default; default; gana la del dominio. Nunca contenido fuera de `/pages`. |
| CA-11 | Arrancar con `BACKSOON_REFRESH=5` y crear una página desde la plantilla. | `content="5"` y `Retry-After: 5`. |
| CA-13 | Cambiar a `error_page 502 504 …`; aplicación que responde 503. | Se ve el 503 propio de la aplicación (manual). |
| CA-14 | `grep -E '<script src\|<link \|@import\|url\(http'` sobre `templates/` y `default.html`; subir un HTML con `<link href="https://…">`. | Sin coincidencias; el panel muestra el aviso. |
| CA-15 | `docker stats --no-stream`, `docker image ls`, `docker exec backsoon ps`. | Dentro de los límites de RNF-1 y RNF-2. |
| CA-16 | Con `backsoon` parado: `docker exec npm nginx -t`, `nginx -s reload` y `docker restart npm`. | Todo correcto; todas las aplicaciones responden. Con el panel parado, `backsoon` sigue sirviendo páginas. |
| CA-17 | Sin sesión: `GET /`, `GET /pages/new`, `POST /pages`. Arrancar el panel sin `BACKSOON_ADMIN_USER`. | Redirección a `/login` o 401; el panel no arranca (código ≠ 0). |
| CA-18 | Crear 3 páginas con destinos variados y abrir `/`. | Listado con las 3 + la página por defecto, sus destinos, fecha y vista previa. |
| CA-19 | Subir un `.html` válido; subir uno de 600 KB; subir uno que no es UTF-8; crear una desde la plantilla. | Alta correcta; rechazo; rechazo; alta correcta. |
| CA-20 | Destinos `APP.Test`, `app:99999`, `a b`, y un destino ya asignado. | Se normaliza a `app.test`; rechazo; rechazo; pide confirmación y lo mueve. |
| CA-21 | Vista previa de un HTML con `<script>alert(1)</script>`; `POST` sin token CSRF. | No se ejecuta nada; 403. |
| CA-22 | Test unitario de publicación con un fallo a mitad de escritura. | Nunca queda un fichero parcial en `backsoon_pages`. |
| CA-23 | Instalación desde cero siguiendo el README en un host limpio. | 4 pasos; CA-1 pasa (manual). |
| CA-24 | 6 logins fallidos seguidos desde la misma IP. | El sexto se rechaza aunque la contraseña sea correcta, hasta pasados 15 min. |
| CA-25 | Rollback: renombrar `server_proxy.conf` y recargar NPM. | NPM recarga sin error y vuelve al comportamiento original. |

## 11. Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| `proxy_pass` con nombre fijo: si `backsoon` no existe al cargar la configuración, falla la recarga de **todo** NPM. | Alta | Crítico | `proxy_pass` por variable (§7.3); CA-16. |
| Recursión de `proxy_intercept_errors`: el 503 de `backsoon` se vuelve a interceptar y se sirve el 503 genérico de nginx. | Alta | Alto | `proxy_intercept_errors off` en `@backsoon`; CA-1. |
| `add_header` sin `always` no se aplica a respuestas 503. | Alta | Medio | `always` en `backsoon`; ninguna cabecera en NPM. |
| POST a una aplicación caída termina en 405. | Media | Medio | `error_page 503 /__backsoon` (a URI → GET); CA-9. |
| `$server`/`$port` no disponibles en `@backsoon` en alguna versión de NPM. | Baja | Medio | Validar en U3; si fallan, solo funciona la selección por dominio (degradación aceptable). Repetir `make e2e` tras cada actualización de NPM. |
| Error de sintaxis o directiva duplicada en `server_proxy.conf` (p. ej. en el "Advanced" de un proxy host) deja NPM sin recargar. | Media | Alto | `nginx -t` antes de cada recarga; despliegue con `.new` y rollback por renombrado (§13). |
| Una aplicación devuelve 503 legítimo (rate limit) y se muestra mantenimiento. | Media | Medio | RF-10: variante `502 504` documentada. |
| Una aplicación que acepta TCP pero se cuelga tarda 60 s en mostrar mantenimiento (timeout de NPM). | Baja | Bajo | Documentado; no se cambian timeouts globales. |
| **Panel expuesto a Internet**: fuerza bruta o robo de sesión. | Media | Alto | bcrypt, rate limit, cookies seguras, CSRF, solo HTTPS vía NPM; opcional: Access List de NPM o acceso solo desde LAN/VPN. |
| **HTML subido malicioso** (XSS en el panel o en el dominio de la aplicación). | Media | Medio | Vista previa en `iframe sandbox` + CSP (RF-18); solo el administrador puede subir; aviso con `<script>` (RF-17). |
| Credenciales en claro en las variables de Portainer. | Media | Medio | Recomendar `BACKSOON_ADMIN_PASSWORD_HASH`; aviso en el log si se usa la contraseña en claro. |
| Bind mount relativo en un stack git de Portainer: las páginas se pierden al redesplegar. | Alta | Medio | Volúmenes con nombre (`backsoon_pages`, `backsoon_meta`). |
| HSTS y cabeceras de seguridad de NPM no se heredan dentro de `@backsoon`. | Media | Bajo | Aceptable: la respuesta es efímera y `no-store`. |
| Si hay CDN delante, un 502 de origen muestra la página del CDN. | Baja | Bajo | El 503 sí pasa; monitores por código de estado, no por palabra clave. |
| Renovación de certificados (`/.well-known/acme-challenge/`). | — | — | La sirve NPM localmente: no le afecta. Repetir CA-1 tras cada actualización de NPM. |

## 12. Decisiones y futuro

**Decisiones tomadas**

- **D1** — Selección de página por dominio y por servicio:puerto, en ese orden, con la página por defecto como último recurso. Los alias (`www.`) son destinos adicionales de la misma página.
- **D2** — Intervalo de recarga único: `BACKSOON_REFRESH` (defecto 10), compartido por `Retry-After` y la plantilla.
- **D3** — HTML por defecto; JSON solo con `Accept: application/json` (RF-12).
- **D4** — Gestión con panel web separado del servidor de páginas; un único administrador definido en las variables de entorno de Portainer; el listado de páginas solo es visible con sesión.
- **D5** — Códigos interceptados por defecto: `502 503 504`, con la variante `502 504` documentada.

- **D6** — El panel `backsoon-admin` se escribe en Go (biblioteca estándar + bcrypt), en imagen `distroless/static`.

**Futuro (v2)**

- Mantenimiento planificado: botón en el panel que crea un flag; `server_proxy.conf` con `if (-f /data/backsoon/flags/$host) { return 503; }` (necesita un volumen `:ro` en NPM y mantener 503 en `error_page`).
- Subida de ZIP: el panel incrusta en el HTML el CSS y las imágenes (como `data:` URI).
- Autocompletar destinos leyendo los proxy hosts desde la API de NPM.
- Varios usuarios o SSO.

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
