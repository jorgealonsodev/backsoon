[English](README.md) | **Español**

# backsoon

![backsoon: cuando una aplicación detrás de Nginx Proxy Manager se está redesplegando, los visitantes ven su propia página de mantenimiento (503 con Retry-After, misma URL) en lugar del 502 Bad Gateway genérico de NPM](docs/assets/hero.png)

Una página de mantenimiento amable para aplicaciones detrás de Nginx Proxy Manager (NPM). Cuando un upstream está caído, NPM entrega la petición a `backsoon`, un nginx estático que responde `503` con la página adecuada para ese dominio o destino. El diseño y los requisitos están en [PRD.md](PRD.md) (en inglés).

> **Estado: en desarrollo.** El `backsoon` estático y el snippet global de NPM están verificados (aceptación local de extremo a extremo y verificación funcional independiente; el propietario dejó el snippet sin revisión nativa tras un fallo de las herramientas, lo cual no equivale a una aprobación nativa). El panel `backsoon-admin` está implementado y superó su suite de extremo a extremo (388 comprobaciones a través de un NPM real) y una verificación independiente. No se ha realizado ningún despliegue en producción. La guía de instalación ([docs/install.es.md](docs/install.es.md)) se ensayó en un host y una red aislados, sin TLS ni las interfaces reales de Portainer y NPM. El workflow de CI ejecuta lint y pruebas en cada push; las suites de extremo a extremo se ejecutan bajo demanda.

| Pieza | Estado |
|-------|--------|
| Servicio nginx estático `backsoon` (`nginx/`, `templates/`, `docker-compose.yml`) | implementado |
| Arnés local de pruebas de NPM (`compose.test.yml`, [docs/npm-harness.md](docs/npm-harness.md)) | unidad aparte, consulta su documento (en inglés) |
| `npm/server_proxy.conf` y `make e2e` (`test/e2e.sh`) | aceptación local y comprobaciones funcionales independientes superadas; omisión de revisión nativa autorizada por el propietario |
| Panel `backsoon-admin` (`admin/`, `make e2e-admin`) | implementado; verificación de extremo a extremo e independiente superadas |
| CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) | lint y pruebas en cada push y pull request; `make e2e` por ejecución manual |
| Guía de instalación ([docs/install.es.md](docs/install.es.md)) | escrita; ensayo en host limpio superado (CA-23), producción todavía no |

## Instalación

El caso habitual es un stack de Portainer desde Git junto a tu Nginx Proxy Manager (NPM), con el fallback de NPM instalado automáticamente. Sigue la **[ruta rápida de la guía de instalación](docs/install.es.md#ruta-rápida-stack-de-portainer-desde-git)**. En resumen:

1. Localiza la red de Docker que comparte NPM, su rango de direcciones y la carpeta de datos de NPM en el host.
2. Genera el hash de la contraseña y el secreto de sesión, y define las variables ([Configuración](#configuración), [Panel de administración](#panel-de-administración)).
3. Despliega el stack en Portainer: construye sus propias imágenes a partir de este repositorio.
4. Crea el proxy host del panel en NPM (`backsoon-admin:8080`, SSL, Force SSL); al guardarlo se activa el fallback.
5. Detén una aplicación para ver su página de mantenimiento.

La [parte de referencia de la guía](docs/install.es.md#referencia) cubre `docker compose` sin Portainer, la instalación manual del snippet, la [verificación](docs/install.es.md#verificación), [qué ocurre cuando `backsoon` o el panel están detenidos](docs/install.es.md#cuando-backsoon-o-el-panel-está-detenido), la [reversión](docs/install.es.md#reversión) y [qué hacer después de actualizar NPM](docs/install.es.md#después-de-actualizar-npm).

## Ruta rápida: probar el servicio por separado

Necesita Docker. Elige cualquier puerto libre de loopback; el nombre del volumen de abajo es desechable.

```sh
docker build -t backsoon:try -f nginx/Dockerfile .
docker volume create backsoon_try_pages
docker run --rm -v backsoon_try_pages:/pages --entrypoint sh backsoon:try /docker-entrypoint.d/45-backsoon-seed.sh
docker run -d --name backsoon-try -v backsoon_try_pages:/pages:ro -p 127.0.0.1:8088:80 backsoon:try
curl -si http://127.0.0.1:8088/any/path      # 503 + Retry-After + Cache-Control: no-store + X-Backsoon: 1
curl -si -H 'Accept: application/json' http://127.0.0.1:8088/   # 503 + {"status":"maintenance","retry_after":10}
```

Limpia con `docker rm -f backsoon-try && docker volume rm backsoon_try_pages`.

Producción usa las mismas piezas a través de `docker-compose.yml`: un `backsoon-init` de una sola ejecución siembra el volumen, y después `backsoon` lo sirve en modo de solo lectura en la red externa `proxy` (o `BACKSOON_PROXY_NETWORK`), sin puertos publicados (primero `docker network create proxy`, y conecta NPM a ella).

## Cómo se elige una página

| Orden | Origen | Archivo en el volumen `backsoon_pages` |
|-------|--------|-----------------------------------------|
| 1 | Cabecera `Host` (minúsculas, sin puerto) | `/pages/host/<host>.html` |
| 2 | `X-Backsoon-Target: service:port` (lo envía NPM) | `/pages/target/<service>_<port>.html` |
| 3 | predeterminada | `/pages/default.html` |
| 4 | no existe nada | `503` con cuerpo vacío (nunca `404`) |

Los hosts o destinos no válidos nunca llegan al sistema de archivos: recurren a la página predeterminada (el propio nginx responde `400` a los hosts que contienen `/`). Las páginas se leen en cada petición, así que añadir, cambiar o eliminar un archivo surte efecto de inmediato.

## Configuración

Copia [`env.example`](env.example) a `.env` y edita la copia privada. En Portainer, introduce los valores en **Environment variables** o usa **Load variables from .env file** para subirla. No subas nunca credenciales reales al repositorio: los archivos `.env` y `.env.*` se ignoran; el ejemplo público no contiene secretos.

El ejemplo enumera todas las variables que lee el stack. Sus secretos están en blanco a propósito: complétalos en tu copia privada (consulta [Panel de administración](#panel-de-administración)).

| Variable | Valor por defecto | Significado |
|----------|-------------------|-------------|
| `BACKSOON_REFRESH` | `10` | Segundos que se usan para `Retry-After`, el `retry_after` del JSON y el `<meta http-equiv="refresh">` de la página. Entero de 1 a 99999; cualquier otro valor detiene el contenedor al arrancar. Compartida con el panel. |
| `BACKSOON_ADMIN_USER` | ninguno | Nombre de usuario del propietario del panel. Obligatoria para el panel. Los demás usuarios se añaden en el propio panel (consulta [Usuarios y roles](#usuarios-y-roles)). |
| `BACKSOON_ADMIN_PASSWORD_HASH` | ninguno | Hash bcrypt de la contraseña del propietario. Es la opción preferida; prevalece si se definen ambas variables de contraseña. |
| `BACKSOON_ADMIN_PASSWORD` | ninguno | Alternativa en texto plano (máximo 72 bytes). Se acepta, con una advertencia en el log del panel. |
| `BACKSOON_SESSION_SECRET` | ninguno | Secreto aleatorio de al menos 32 bytes que firma las cookies de sesión de 12 horas. Obligatoria para el panel. Cerrar sesión revoca la sesión al instante, pero el panel guarda esa lista en memoria: tras reiniciar el panel, una copia de una cookie de una sesión cerrada vuelve a funcionar hasta que terminen sus 12 horas. Cambia este secreto para terminar todas las sesiones. |
| `BACKSOON_ADMIN_HOST_BIND` | `127.0.0.1` | Dirección del host en la que se publica el puerto del panel. Usa `0.0.0.0` solo si NPM u otra persona debe acceder a través de la IP del host; `127.0.0.1` lo mantiene local (túnel SSH). |
| `BACKSOON_ADMIN_HOST_PORT` | `8181` | Puerto del host publicado para el panel (puerto del contenedor 8080). |
| `BACKSOON_NPM_CUSTOM_DIR` | ninguno | Ruta en el host del directorio `/data/nginx/custom` de NPM, por ejemplo `/docker/nginx-proxy-manager/data/nginx/custom`. Si se define, el servicio de una sola ejecución `backsoon-npm-snippet` instala `server_proxy.conf` allí en cada despliegue (los archivos sin cambios se dejan como están; un `server_proxy.conf` que no sea de backsoon nunca se sobrescribe). NPM lo aplica en su siguiente recarga: guarda cualquier proxy host o reinicia NPM. |
| `BACKSOON_NPM_INTERCEPT_503` | `true` | `false` instala la variante que deja en paz las respuestas `503` de las aplicaciones (intercepta solo `502` y `504`). |
| `BACKSOON_PROXY_NETWORK` | `proxy` | Nombre de la red externa de Docker existente que se comparte con NPM, por ejemplo una red de NPM como `npm-nw`. |
| `BACKSOON_TRUSTED_PROXIES` | ninguno | Direcciones IP o rangos CIDR, separados por comas o espacios, autorizados a establecer la cabecera `X-Real-IP` que sirve de clave para el límite de intentos de inicio de sesión; defínela con el rango de la red `proxy`, por ejemplo `172.18.0.0/16`. Un peer fuera de la lista queda limitado por su propia dirección. Sin definir, el panel cree en `X-Real-IP` de cualquier peer. Una entrada no válida detiene el panel al arrancar. |

Sin un usuario, una contraseña o hash, y un secreto, el panel termina al arrancar con un mensaje que nombra la variable que falta; nunca se ejecuta sin inicio de sesión. `backsoon` no depende del panel y sigue sirviendo páginas si el panel está detenido o mal configurado.

El `default.html` provisional se genera a partir de `templates/maintenance.html` mediante `backsoon-init`. Si `default.html` ya existe, la siembra no cambia nada, de modo que los redespliegues conservan tus páginas (y un valor de refresco antiguo dentro de una página existente). Los inicializadores concurrentes generan archivos temporales exclusivos en ese volumen y publican con un enlace duro que no sobrescribe; un inicializador que pierde la carrera no puede modificar la página publicada.

## Panel de administración

`backsoon-admin` gestiona las páginas: crea una a partir de la plantilla o sube un `.html` autocontenido (UTF-8, máximo 512 KB), asígnala a dominios y destinos `service:port`, previsualízala en un sandbox, reemplázala o elimínala. Cada cambio se publica de forma atómica en el volumen `backsoon_pages` y se sirve en la siguiente petición, sin reiniciar. El panel responde a la pregunta «¿qué página muestra este destino?» en el mismo orden que usa `backsoon`.

El panel está disponible en inglés y español. Elige el idioma a partir de la cookie `backsoon_lang` si existe; si no, de la cabecera `Accept-Language` del navegador (por ejemplo, `es-ES` selecciona español); si no, inglés. El selector **ES / EN** de la barra de navegación, y de la página de inicio de sesión, lo cambia sin JavaScript y recuerda la elección en la cookie `backsoon_lang` durante aproximadamente un año (`HttpOnly`, `Secure`, `SameSite=Strict`). Solo se traduce el panel: su log sigue en inglés, y la página de mantenimiento que ven los visitantes conserva el idioma que tú le des.

1. **Crea el hash de la contraseña.** Usa bcrypt con coste 12 (el valor por defecto de `htpasswd`, 5, es demasiado débil para un inicio de sesión expuesto a internet):

   ```sh
   docker run --rm httpd:2.4-alpine htpasswd -nbBC 12 admin 'your-password' | cut -d: -f2
   ```

   La salida empieza por `$2y$12$` y tiene 60 caracteres. Antepón un espacio al comando o borra después el historial de tu shell, ya que contiene la contraseña.

2. **Define las variables** en Portainer (Environment variables) o en tu `.env` privado. Un hash bcrypt contiene `$`, que Docker Compose trata como el inicio de una variable cuando lee un archivo env. Comprobado con Docker Compose 5.6.0:

   | Cómo se indica el hash | Resultado |
   |------------------------|-----------|
   | `.env`: `BACKSOON_ADMIN_PASSWORD_HASH='$2y$12$…'` (comillas simples) | intacto |
   | `.env`: cada `$` escrito como `$$` | intacto |
   | entorno del proceso (`export …` antes de `docker compose up`) | intacto |
   | `.env` sin comillas, o con comillas dobles | **truncado** a `$2y$12` |

   Un hash truncado no provoca un fallo silencioso de inicio de sesión: el panel se niega a arrancar con `BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`. Si ves ese mensaje después de introducir el hash en Portainer, vuelve a introducirlo con cada `$` escrito como `$$`.

3. **Genera el secreto de sesión**, por ejemplo con `openssl rand -hex 32`.

4. **Publica el panel a través de NPM** con un proxy host como `backsoon.example.com` → `backsoon-admin:8080`, con SSL. La cookie de sesión es `Secure`, así que el panel solo permite iniciar sesión por HTTPS o en `http://localhost`: su puerto publicado en el host (`127.0.0.1:8181` por defecto, consulta `BACKSOON_ADMIN_HOST_BIND`) está pensado para un túnel SSH, no para acceso `http://` sin cifrar. Las peticiones a ese puerto llegan al panel desde la dirección de la puerta de enlace de la red, que el rango recomendado de `BACKSOON_TRUSTED_PROXIES` incluye, así que quien pueda alcanzar el puerto también puede establecer `X-Real-IP`: mantenlo en `127.0.0.1`. El panel confía en la cabecera `X-Real-IP` que establece NPM para el límite de intentos de inicio de sesión (5 intentos fallidos por IP cada 15 minutos). Sin `BACKSOON_TRUSTED_PROXIES`, el panel acepta esa cabecera de cualquier peer, así que cualquier contenedor conectado a la red `proxy` podría falsificarla y eludir el límite (bcrypt sigue siendo el freno). Define `BACKSOON_TRUSTED_PROXIES` con el rango de la red `proxy`, que imprime `docker network inspect proxy --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'`, de modo que solo esa red pueda establecerla; el panel registra en su log los rangos de confianza al arrancar. La dirección propia de NPM puede cambiar entre reinicios, así que es preferible el rango de la red a una única dirección. En cualquier caso, mantén en esa red solo contenedores de confianza. Detrás de una CDN, todos los visitantes pueden compartir una misma dirección, de modo que unos pocos inicios de sesión fallidos pueden bloquear al administrador durante 15 minutos. Una Access List de NPM o un host accesible solo desde la LAN o una VPN añade otra capa.

El panel guarda sus metadatos en el volumen `backsoon_meta` (`sites.json` más el HTML original de cada página, y `users.json` cuando añades usuarios) y vuelve a publicar cada página a partir de ellos al arrancar, lo que repara un `backsoon_pages` inconsistente. Se ejecuta con el uid 65532 sobre un sistema de archivos raíz de solo lectura y sin ninguna capability; `backsoon-init` entrega el volumen de páginas a ese usuario en cada despliegue. El sistema visual del panel se describe en [DESIGN.md](DESIGN.md) (en inglés).

### Usuarios y roles

El panel tiene dos roles.

- **Propietario.** El usuario de `BACKSOON_ADMIN_USER`, con la contraseña de `BACKSOON_ADMIN_PASSWORD_HASH` o `BACKSOON_ADMIN_PASSWORD`. Gestiona usuarios y páginas. No se puede eliminar, cambiar de nombre ni cambiar su contraseña desde el panel: esa contraseña vive en las variables del stack, y al cambiarla allí (y volver a desplegar) terminan las sesiones del propietario.
- **Editor.** Cada usuario añadido en el panel. Los editores gestionan páginas exactamente igual que el propietario y cambian su propia contraseña. Nunca ven la gestión de usuarios: la barra de navegación no tiene la entrada **Usuarios**, y el servidor responde `403` a cualquier petición hacia ella, lleve lo que lleve.

**Gestionar usuarios** (solo el propietario, **Usuarios** en la barra de navegación):

- **Añade un usuario** con un nombre de usuario y una contraseña inicial escrita dos veces. Los nombres tienen de 1 a 64 caracteres entre `a-z 0-9 . _ -`, se comparan sin distinguir mayúsculas y se guardan en minúsculas; cada uno es único y nunca coincide con el del propietario. Las contraseñas tienen al menos 10 caracteres y como máximo 72 bytes (el límite de bcrypt). Tú eliges la contraseña inicial y se la pasas al usuario.
- **Establece una nueva contraseña** para un usuario, o **elimina** un usuario. Cada acción pide una confirmación antes y termina de inmediato todas las sesiones de ese usuario, también tras reiniciar el panel.
- La lista muestra el nombre de cada usuario, cuándo se creó y cuándo cambió su contraseña por última vez.

**Contraseña propia.** Cada editor abre **Mi contraseña** en la barra de navegación, indica la contraseña actual y la nueva dos veces, y la cambia. Sus otras sesiones terminan; el navegador desde el que hizo el cambio sigue con la sesión abierta. La comprobación de la contraseña actual tiene límite de intentos (5 fallos cada 15 minutos por usuario). La página del propietario explica dónde se define su contraseña, en lugar de ofrecer un formulario.

**Almacenamiento y log.** Los usuarios se guardan en `users.json` en el volumen `backsoon_meta`, escrito de forma atómica con modo `0600`, solo como hashes bcrypt de coste 12. Un archivo corrupto o no válido detiene el panel al arrancar con un mensaje que lo nombra; corrige el archivo, o elimínalo para borrar todos los usuarios añadidos. Inclúyelo en las copias de seguridad de `backsoon_meta`. El inicio de sesión usa el mismo límite de intentos y el mismo mensaje de error único para un usuario desconocido y una contraseña incorrecta. El log nombra al usuario en cada inicio de sesión, quién añadió o eliminó un usuario o estableció una contraseña (y para quién), los cambios de contraseña propia y al usuario que hay detrás de cada cambio de página; nunca contiene una contraseña, un hash ni una cookie de sesión.

**Actualizar desde una versión sin usuarios.** Las cookies de sesión ahora incluyen al usuario, así que las sesiones emitidas antes de la actualización dejan de funcionar una vez: todos, en la práctica el propietario, vuelven a iniciar sesión tras el primer arranque de la nueva versión. Si más adelante pones en `BACKSOON_ADMIN_USER` el nombre de un usuario añadido, gana el propietario y ese usuario ya no podrá iniciar sesión; elimínalo o cámbiale el nombre antes.

## Comandos

| Comando | Qué hace |
|---------|----------|
| `make lint` | `docker compose config`, `nginx -t` sobre la configuración renderizada, comprobaciones de imágenes fijadas y de páginas autocontenidas, sintaxis de shell, cableado del panel, `gofmt` y `go vet` para `admin/`. |
| `make test` | Construye la imagen y ejecuta `test/unit.sh` (pruebas con curl contra contenedores desechables en puertos efímeros de loopback, nombres `bs-unit-*`, eliminados al salir, incluida la siembra concurrente), y después las pruebas Go del panel. |
| `sh test/seed-race.sh` | Regresión específica de la siembra concurrente con dos entrypoints de init reales y un lector HTTP de solo lectura; comprueba la publicación, que no se sobrescribe y la limpieza. |
| `make e2e-up` / `make e2e-down` | Inicia / elimina el arnés de NPM. Consulta [docs/npm-harness.md](docs/npm-harness.md) (en inglés). |
| `make e2e` | Las dos suites desechables con NPM real, una tras otra: `make e2e-npm` (enrutamiento sano, fallback por host/destino/predeterminado, POST/HEAD/JSON, 502/503/504, backend ausente/reinicio/recuperación, reversión, variante de 503 legítimo, umbral de latencia) y `make e2e-admin` (el panel a través de NPM: inicio de sesión, límite de intentos, CSRF, usuarios y roles, subidas, destinos, sandbox de previsualización, publicación visible en la siguiente petición). No las ejecutes nunca en paralelo: comparten los puertos de loopback. |

`make lint` y `make test` necesitan Go 1.26 o posterior además de Docker.

## Detalles

| Tema | Decisión |
|------|----------|
| Imagen | `nginx:1.30.5-alpine-slim`, fijada por digest en `nginx/Dockerfile`. Su entrypoint incluye `envsubst`, así que no hace falta la alternativa `nginx:alpine` simple. |
| Renderizado de la configuración | `nginx/default.conf.template` lo renderiza el entrypoint oficial. `NGINX_ENVSUBST_FILTER` limita la sustitución a `${BACKSOON_REFRESH}`, de modo que las capturas `${...}` propias de nginx sobreviven. |
| Ejecución | `worker_processes 1` en `nginx/nginx.conf`; nginx es el único proceso. |
| Logs | `host target status file` por petición, sin dirección del cliente. Las páginas sin archivo registran el URI del asistente de respuesta vacía. |
| Contexto de build | Raíz del repositorio, para que la imagen reutilice `templates/maintenance.html`. |

## Configuración global de NPM

`npm/server_proxy.conf` es el único archivo de configuración de NPM que se necesita. NPM debe compartir la red externa `proxy` con `backsoon`. Cada proxy host conserva su upstream normal y la **configuración avanzada vacía**.

Después de elegir la política de interceptación, instala el archivo en tu contenedor de NPM y valida antes de recargar:

```sh
docker cp npm/server_proxy.conf <npm>:/data/nginx/custom/server_proxy.conf
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

El fallback con nombre pasa a `backsoon` el host original, la cabecera `Accept` y el `$server:$port` de NPM. El `proxy_pass` con variable aplaza la resolución DNS, y la interceptación se desactiva dentro del fallback para que el 503 de `backsoon` no se vuelva a interceptar. La petición interna a `backsoon` apunta siempre al URI fijo `/` (la `/` final de la URL en la variable), sea cual sea el URI de la aplicación, de modo que una ruta de la aplicación como `/healthz` o `/__backsoon` nunca puede llegar a las ubicaciones privadas de `backsoon`; la URL del navegador no cambia y no se redirige nada. Solo `backsoon` añade las cabeceras de mantenimiento.

### Cuando `backsoon` no está disponible

Degradación acotada: solo el fallback tiene tiempos de espera cortos (2 s de resolución, 2 s de conexión, 5 s de inactividad por envío/lectura, un único intento); tus aplicaciones conservan los tiempos de espera propios de NPM. Si `backsoon` está detenido, ausente o inalcanzable, el fallback falla dentro de ese límite y NPM responde con su `502` genérico nativo (sin cabeceras ni cuerpo de mantenimiento).

nginx normalmente se detiene tras el primer salto de `error_page` (`recursive_error_pages off`), así que un fallback fallido terminaría como un `504` lento o un bloqueo sobre una dirección en caché obsoleta. Por eso el archivo activa `recursive_error_pages` y fija la cadena en `server → @backsoon → @backsoon_unavailable`. `@backsoon` declara su propia lista `error_page 502 504` que apunta a `@backsoon_unavailable`, el cual devuelve `502` y declara su propia lista inerte. Cada lista propia reemplaza a la heredada, de modo que ningún salto puede volver a `@backsoon`. La única lista editable de negocio sigue siendo la única línea `error_page … @backsoon;`.

El log de peticiones de NPM muestra un `502` HTTP controlado de una aplicación como `upstream_status` `502 : 503` con estado de cliente `503` (observado en NPM 2.16.0 con el fixture de pruebas). Un fallo de resolución DNS de la aplicación puede registrar `-, 503` en su lugar, así que no des por hecho un único formato para todas las causas de `502`.

### Aplicaciones con respuestas 503 legítimas

Edita solo la única línea de negocio `error_page … @backsoon;` del archivo global copiado para excluir el 503:

```nginx
error_page 502 504 =503 @backsoon;
```

Valida y recarga NPM de nuevo (nginx aplica una recarga de forma asíncrona: los workers antiguos siguen sirviendo hasta que terminan). Entonces tu aplicación conserva su propio cuerpo y cabeceras de 503; 502/504 siguen usando el mantenimiento. Elige esta política antes de instalar el archivo de forma global, ya que de lo contrario se intercepta un 503 generado por la aplicación.

### Desactivar con seguridad

```sh
docker exec <npm> mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.disabled
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

No se modifica ningún proxy host ni página almacenada. Copia de nuevo el archivo del repositorio y recarga para activarlo otra vez.

## Verificación y alcance restante

```sh
make lint
make test
BACKSOON_TEST_HTTP_PORT=18080 BACKSOON_TEST_ADMIN_PORT=18181 make e2e
```

Usa puertos de loopback libres. `make e2e` requiere un proyecto `backsoon-*` nuevo, siembra solo fixtures de prueba y elimina sus contenedores y volúmenes al salir. Usa el mismo Dockerfile, configuración y entrypoint que producción, y un fixture de upstream privado y determinista. Consulta [la guía del arnés](docs/npm-harness.md) (en inglés).

`make lint` envuelve el snippet de NPM en un `server{}` solo de sintaxis; `make e2e` también comprueba la sintaxis real del nginx de NPM y la recuperación dinámica.

`make e2e` es un procedimiento fijo, sin umbral ni opción para omitir pasos. Las comprobaciones funcionales se ejecutan primero; si alguna falla, la ejecución termina con fallo y se omite el paso de rendimiento. Cada recarga (instalación, variante, reversión, backend ausente) espera a que se retire la generación anterior de workers de NPM, porque `nginx -T` o una primera respuesta correcta no demuestran que todos los workers usen la nueva configuración. Antes de la comprobación del backend ausente, un proxy host desechable apunta al fixture de pruebas activo en un puerto cerrado (rechazo inmediato de la conexión), de modo que nunca se consulta el `app1` detenido durante el calentamiento; una recarga explícita (con la barrera de generación) proporciona workers nuevos con la caché de `app1` en frío. Después, 150 peticiones fijas a través de ese host deben devolver cada una el 503 de mantenimiento con `X-Backsoon: 1` y la página, lo que hace que cada worker actual de NPM use y guarde en caché la dirección de `backsoon`. Cada worker debe mostrar el rechazo de la conexión (`connect() failed (111`) para ese host en el log de errores propio de NPM; si una petición falla, algún worker no está cubierto o se supera la ventana, la ejecución registra el estado de salida y el stderr de curl y los logs de NPM, y falla antes de esa comprobación en lugar de darla por buena. La única petición que sigue conserva el límite original de 10 s del cliente.

RNF-5 usa ocho bloques alternos A/B/A/B/A/B/A/B (A: archivo global desactivado, B: archivo canónico activado), una barrera de generación de workers en cada cambio, 20 peticiones sanas descartadas y 50 registradas por bloque (200 registros por modo), y el umbral del PRD `median(B) ≤ median(A) × 1.05` sobre todos los registros. Se imprimen todas las medianas de los bloques y la agregada, todas las muestras se conservan en `.odd-checks/` (ignorado por git), ninguna petición sana puede añadir una línea al log de acceso de `backsoon`, y la ejecución termina con el archivo canónico activado. Se evalúa una vez por ejecución, sin repetirse hasta que pase. La aceptación local registrada superó 111 comprobaciones; una verificación funcional aislada e independiente superó 101 comprobaciones sin repetir el benchmark. Los ocho bloques dieron medianas agregadas de 484 µs (A) y 474,5 µs (B), una diferencia de −1,9628 %, que cumple el umbral del 5 % en esa ejecución. Esto no es evidencia de una aceleración universal. El fallo de +5,47 % de la ejecución serie anterior se conserva, no se oculta.

Las comprobaciones reales de redespliegue en producción siguen aplazadas a U8; los límites del ensayo de la guía de instalación se enumeran en [qué se ha verificado](docs/install.es.md#qué-se-ha-verificado). Las comprobaciones de navegador de la página estática cubren 320/1280 px en modo claro y oscuro; la medición manual en el navegador del tiempo de recuperación de CA-2 no se ha verificado. TLS y las pruebas en dispositivos físicos no forman parte del arnés local de NPM.
