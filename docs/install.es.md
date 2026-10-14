[English](install.md) | **Español**

# Instalar backsoon

Una guía para quienes alojan sus propios servicios y ya ejecutan [Nginx Proxy Manager](https://nginxproxymanager.com/) (NPM) en Docker. Empieza con una ruta corta para el caso habitual; todo lo demás está en la [Referencia](#referencia) que viene después.

## Ruta rápida: stack de Portainer desde Git

Portainer despliega backsoon desde este repositorio, y el stack instala por ti el fallback de NPM.

### Qué necesitas

- Un host de Docker con Portainer.
- NPM ejecutándose en un contenedor, con su carpeta de datos enlazada al host (un bind mount, no un `tmpfs`).
- Un nombre DNS para el panel, por ejemplo `backsoon.example.com`, que apunte a NPM.

### 1. Localiza tres datos de tu NPM

Ejecuta estos comandos en el host de Docker. Sustituye `<npm>` por el nombre del contenedor de NPM.

| Dato | Comando | Qué anotar |
|------|---------|------------|
| La red de Docker que comparte NPM | `docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' <npm>` | El nombre de la red que NPM comparte con sus aplicaciones (no su red de base de datos), por ejemplo `npm-nw`. |
| Su rango de direcciones | `docker network inspect <network> --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'` | Un rango como `172.18.0.0/16`. |
| La carpeta de datos de NPM en el host | `docker inspect -f '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}' <npm>` | La ruta de la línea que termina en `-> /data`, por ejemplo `/docker/nginx-proxy-manager/data`. Añádele `/nginx/custom`. |

Si NPM no tiene una red propia que compartir, crea una y conecta NPM a ella: `docker network create proxy && docker network connect proxy <npm>`. Después usa `proxy` como nombre de la red. El [paso 1 de la Referencia](#paso-1-la-red-proxy-con-npm-conectado) explica cómo hacer permanente esa conexión.

### 2. Genera el hash de la contraseña y el secreto de sesión

```sh
docker run --rm httpd:2.4-alpine htpasswd -nbBC 12 admin 'your-password' | cut -d: -f2
openssl rand -hex 32
```

El primer comando imprime el hash bcrypt de tu contraseña (empieza por `$2y$12$`). Empieza la línea con un espacio para que tu shell no guarde la contraseña en su historial. El segundo imprime un secreto aleatorio. Guarda ambos para el paso siguiente.

### 3. Define las variables

Copia este bloque y rellena los valores. En Portainer, añade cada línea en *Environment variables*, o guarda el bloque como un archivo y usa *Load variables from .env file*.

```sh
# La red de Docker que comparte NPM (dato 1). El valor por defecto es "proxy".
BACKSOON_PROXY_NETWORK=npm-nw
# El rango de direcciones de NPM (dato 2). Solo esta red puede indicar al panel la dirección de un visitante.
BACKSOON_TRUSTED_PROXIES=172.18.0.0/16
# La carpeta de datos de NPM en el host (dato 3) más /nginx/custom. Cada despliegue instala allí el fallback.
BACKSOON_NPM_CUSTOM_DIR=/docker/nginx-proxy-manager/data/nginx/custom
# El nombre de usuario con el que iniciarás sesión en el panel.
BACKSOON_ADMIN_USER=admin
# El hash bcrypt del paso 2, no la contraseña en sí.
BACKSOON_ADMIN_PASSWORD_HASH=$2y$12$paste-your-hash-here
# El secreto aleatorio del paso 2. Firma las sesiones de inicio de sesión.
BACKSOON_SESSION_SECRET=paste-your-secret-here
```

Sobre el hash: pégalo tal cual. Si después el panel se niega a arrancar con `BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`, introdúcelo de nuevo con cada `$` escrito como `$$` (Compose interpreta un `$` suelto como el inicio de una variable y corta el hash).

Variables opcionales, ninguna necesaria para arrancar: `BACKSOON_REFRESH` (segundos entre reintentos automáticos, por defecto `10`), `BACKSOON_NPM_INTERCEPT_503` (`false` deja en paz las respuestas `503` propias de una aplicación), `BACKSOON_ADMIN_HOST_BIND` y `BACKSOON_ADMIN_HOST_PORT` (dónde se publica el panel en el host, por defecto `127.0.0.1:8181`). Todas se describen en [Configuración](../README.es.md#configuración) del README.

### 4. Despliega el stack

En Portainer: *Stacks → Add stack → Repository*. Indica la URL y la rama de este repositorio, mantén la ruta de compose `docker-compose.yml`, añade las variables del paso 3 y despliega. La primera construcción tarda unos minutos.

Después comprueba los contenedores en el host de Docker:

```sh
docker ps -a --filter name=backsoon --format '{{.Names}}: {{.Status}}'
docker logs backsoon-npm-snippet
```

`backsoon` y `backsoon-admin` deben estar `healthy`, `backsoon-init` y `backsoon-npm-snippet` deben estar `Exited (0)`, y el segundo comando debe decir `installed`, seguido de `ACTION NEEDED: reload Nginx Proxy Manager…` (el paso 5 hace esa recarga); en un redespliegue posterior dice `unchanged; no reload needed`. Si `backsoon-admin` se reinicia continuamente, `docker logs backsoon-admin` indica qué variable falta o no es válida.

### 5. Crea el proxy host del panel

En NPM, añade un **proxy host**. **Guardarlo es lo que activa el fallback**: NPM lee el archivo que instaló el stack en su siguiente recarga, y guardar un host la provoca.

| Campo | Valor |
|-------|-------|
| Domain | `backsoon.example.com` (el nombre DNS de tu panel) |
| Scheme | `http` |
| Forward hostname | `backsoon-admin` |
| Forward port | `8080` |
| SSL | solicita o selecciona un certificado y activa **Force SSL** |
| Advanced | vacío |

Después abre `https://backsoon.example.com` e inicia sesión con `BACKSOON_ADMIN_USER` y la contraseña que convertiste en hash. El panel solo permite iniciar sesión por HTTPS.

### 6. Comprueba que funciona

1. Detén una de tus aplicaciones: `docker stop <application>`.
2. Abre su dirección en un navegador. Deberías ver una página de mantenimiento en lugar del `502 Bad Gateway` de NPM.
3. Vuelve a iniciarla: `docker start <application>`. La página de mantenimiento reintenta sola cada 10 segundos (`BACKSOON_REFRESH`), así que la aplicación debería volver en unos 10 segundos. Ese intervalo es el valor de diseño; el tiempo de recuperación en un navegador real no se ha medido (consulta [Qué se ha verificado](#qué-se-ha-verificado)).

Todavía no existe ninguna página tuya, así que los visitantes ven la página predeterminada provisional. En el panel, crea una página y asígnale como destino el dominio de la aplicación.

Si algo no coincide, consulta [Verificación](#verificación) para ver las comprobaciones exactas con `curl`, y usa `docker logs` en cada contenedor.

## Referencia

Todo lo que sigue es lectura opcional: las otras formas de instalar, los detalles detrás de la ruta rápida y lo que se ha verificado.

### La instalación en cuatro pasos

La instalación completa, con todas las opciones, tiene **4 pasos**:

| Paso | Qué | Dónde |
|------|-----|-------|
| 1 | Crear la red `proxy` y conectar NPM a ella | Host de Docker |
| 2 | Desplegar el stack (`backsoon-init`, `backsoon`, `backsoon-admin`) | Portainer o `docker compose` |
| 3 | Instalar `npm/server_proxy.conf` en NPM | Contenedor de NPM |
| 4 | Publicar el panel a través de NPM, iniciar sesión y crear páginas | Interfaz de NPM, después el panel |

Después [verifica](#verificación). Para deshacerlo todo, consulta [Reversión](#reversión). Portainer y `docker compose` sin Portainer usan los mismos comandos; solo cambia la forma de iniciar el stack.

### Requisitos previos

- Un host de Docker con Docker Compose v2 (comprobado con Docker 29.9 y Compose 5.6) al que puedas acceder con `docker`, además de acceso a internet para la primera construcción.
- Un contenedor de NPM en ejecución. Las versiones probadas son `jc21/nginx-proxy-manager:2.16.0` y `2.15.1` (ambas suites de extremo a extremo); otras versiones no se han probado (consulta [Después de actualizar NPM](#después-de-actualizar-npm)).
- Registros DNS para el panel (por ejemplo `backsoon.example.com`) y para tus aplicaciones que apunten a NPM.
- Para el propio backsoon no se descarga nada de un registro: el stack **construye** sus dos imágenes a partir de este repositorio (`nginx/Dockerfile` y `admin/Dockerfile`, contexto de build = raíz del repositorio) y las etiqueta como `backsoon:local` y `backsoon-admin:local`. La primera construcción descarga tres imágenes base fijadas (`nginx`, `golang`, `distroless`) y tarda unos minutos.

#### Qué se crea

| Tipo | Nombre | Notas |
|------|--------|-------|
| Contenedores | `backsoon-init`, `backsoon`, `backsoon-admin`, `backsoon-npm-snippet` | `container_name` fijos, por lo que un host solo puede ejecutar un stack de backsoon. `backsoon-init` y `backsoon-npm-snippet` son de una sola ejecución y terminan con código 0. |
| Volúmenes | `backsoon_pages`, `backsoon_meta`, `backsoon_npm_custom` | Nombres fijos. `backsoon_pages` contiene las páginas servidas; `backsoon_meta` contiene el `sites.json` del panel y el HTML original; `backsoon_npm_custom` queda vacío (sustituye al directorio de NPM cuando `BACKSOON_NPM_CUSTOM_DIR` no está definida). |
| Red | `proxy` (externa, o `BACKSOON_PROXY_NETWORK`) | Debe existir antes del despliegue. |
| Puertos | `127.0.0.1:8181` → panel `8080` | `BACKSOON_ADMIN_HOST_BIND` y `BACKSOON_ADMIN_HOST_PORT`. `backsoon` no publica nada. |

`backsoon-init` siembra el `default.html` provisional en `backsoon_pages` (nunca sobrescribe uno existente) y entrega el volumen al uid 65532, el usuario del panel, en cada despliegue. `backsoon` y `backsoon-admin` esperan a que termine correctamente. El panel escucha en el puerto 8080 dentro de la red; `backsoon` escucha en el puerto 80.

### Paso 1: la red `proxy`, con NPM conectado

Si NPM ya comparte una red definida por el usuario a la que quieres que se una backsoon, úsala en lugar de crear `proxy`: define `BACKSOON_PROXY_NETWORK` con su nombre en el paso 2a y omite el resto de este paso. Lista las redes de NPM con:

```sh
docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' <npm>
```

Elige una red cuyos otros miembros sean de confianza (idealmente solo NPM), no la red de base de datos de NPM. Si no, comprueba si la red `proxy` ya existe y créala en caso contrario:

```sh
docker network ls --filter name=^proxy$
docker network create proxy
```

Conecta tu contenedor de NPM a ella:

```sh
docker network connect proxy <npm>
```

`<npm>` es el nombre del contenedor de NPM. Un `connect` manual dura solo lo que dure ese contenedor: hazlo permanente en el propio stack de NPM (en Portainer, edita el stack de NPM) añadiendo la red al servicio de NPM y declarándola como externa:

```yaml
services:
  npm:                 # your existing NPM service
    networks:
      - default        # keep whatever networks it already has
      - proxy
networks:
  proxy:
    external: true
```

NPM llega a `backsoon` y `backsoon-admin` por nombre de contenedor a través de esta red. Conecta solo contenedores de confianza: cualquier contenedor de `proxy` puede acceder al panel (consulta [Panel de administración](../README.es.md#panel-de-administración) en el README para ver la consecuencia sobre el limitador de inicios de sesión).

### Paso 2: despliega el stack

#### 2a. Prepara las variables

Copia [`env.example`](../env.example) a un `.env` privado (está en gitignore) y completa:

- `BACKSOON_ADMIN_USER`: el usuario del panel.
- `BACKSOON_ADMIN_PASSWORD_HASH`: un hash bcrypt de la contraseña.
- `BACKSOON_SESSION_SECRET`: una cadena aleatoria de al menos 32 bytes.

Genera el hash y el secreto, e introduce el hash con las comillas que mantienen intactos sus caracteres `$`, siguiendo los pasos 1 a 3 de [Panel de administración](../README.es.md#panel-de-administración) en el README. Sin un usuario, una contraseña o hash, y un secreto, el panel se niega a arrancar y nombra la variable que falta; `backsoon` sigue sirviendo en cualquier caso. Las demás variables son opcionales: `BACKSOON_REFRESH` (por defecto `10`), `BACKSOON_PROXY_NETWORK` (por defecto `proxy`; la red del paso 1) y `BACKSOON_TRUSTED_PROXIES`. Define esta última con el rango de esa red para que solo la red de NPM pueda establecer la dirección del cliente para el límite de intentos de inicio de sesión:

```sh
docker network inspect proxy --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'
```

(sustituye `proxy` por tu `BACKSOON_PROXY_NETWORK`).

Por ejemplo `BACKSOON_TRUSTED_PROXIES=172.18.0.0/16`. Al arrancar, el panel registra `login rate limit: X-Real-IP trusted from 172.18.0.0/16`; un valor no válido lo detiene con un mensaje que nombra la variable.

#### 2b. Inicia el stack

**Portainer, desde un repositorio Git (recomendado).** *Stacks → Add stack → Repository*: indica la URL y la rama de este repositorio, mantén la ruta de compose `docker-compose.yml` y añade las variables de tu `.env` en *Environment variables* (o *Load variables from .env file*). Portainer clona el repositorio, así que existen los contextos de build. Si el hash se rechaza con `BACKSOON_ADMIN_PASSWORD_HASH is not a valid bcrypt hash`, introdúcelo de nuevo con cada `$` escrito como `$$`.

**Portainer, editor web.** El editor web despliega solo el archivo compose pegado, sin `nginx/`, `admin/` ni `templates/`, así que el build no puede ejecutarse allí. Construye primero las imágenes en el host de Docker y después pega `docker-compose.yml` en el editor; Compose usa las imágenes existentes `backsoon:local` y `backsoon-admin:local` y no construye:

```sh
git clone <repository-url> backsoon && cd backsoon
docker compose build
```

**`docker compose` sin Portainer**, desde la raíz del repositorio (lee `.env` de ese directorio):

```sh
git clone <repository-url> backsoon && cd backsoon
cp env.example .env        # edit it as in 2a
docker compose up -d --build
```

#### 2c. Compruébalo

```sh
docker compose ps -a      # backsoon and backsoon-admin: healthy; backsoon-init: Exited (0)
docker exec backsoon wget -qSO- http://127.0.0.1/ 2>&1 | grep 503
docker exec <npm> curl -sI http://backsoon/
```

El segundo comando imprime la línea de estado `503` (`backsoon` responde 503 con la página predeterminada provisional por diseño). Usa `127.0.0.1`, no `localhost`: dentro del contenedor `localhost` se resuelve primero a `::1` y `backsoon` escucha solo en IPv4, así que `localhost` es rechazado. El `wget` del contenedor además imprime solo la línea de estado de una respuesta de error, por lo que las cabeceras salen del tercer comando: ejecutado desde NPM, demuestra la red compartida y muestra `Retry-After`, `Cache-Control: no-store` y `X-Backsoon: 1`.

Si `backsoon-admin` se está reiniciando, lee `docker logs backsoon-admin`: indica la variable no válida o ausente.

### Paso 3: instala `server_proxy.conf` en NPM

Decide primero si alguna de tus aplicaciones responde legítimamente `503` por sí misma. El archivo incluido intercepta `502`, `503` y `504`. Para dejar en paz las respuestas `503` de las aplicaciones, usa la variante cuya única línea de negocio es `error_page 502 504 =503 @backsoon;`.

#### Automática (recomendada)

Define `BACKSOON_NPM_CUSTOM_DIR` con la ruta **del host** del directorio `/data/nginx/custom` de NPM (para un NPM con `/docker/nginx-proxy-manager/data` enlazado a `/data`: `/docker/nginx-proxy-manager/data/nginx/custom`), y `BACKSOON_NPM_INTERCEPT_503=false` para la variante. En cada despliegue el servicio de una sola ejecución `backsoon-npm-snippet` escribe el archivo de forma atómica (`docker logs backsoon-npm-snippet` dice `installed` o `unchanged`); se niega, sin cambiar nada, si ya hay un `server_proxy.conf` que no enruta a `@backsoon`. `backsoon` y el panel lo esperan, así que una negativa detiene el despliegue, como cualquier otro error de configuración: los contenedores que el despliegue recrea (siempre en el primer despliegue de una versión nueva) permanecen detenidos hasta que corrijas la variable o muevas el archivo ajeno y vuelvas a desplegar. El archivo pertenece a backsoon: cada despliegue reemplaza las ediciones que se le hagan, así que elige la variante con `BACKSOON_NPM_INTERCEPT_503` en lugar de editarlo. Comprueba la ruta: un `BACKSOON_NPM_CUSTOM_DIR` mal escrito hace que Docker cree un directorio vacío que NPM nunca lee. Si NPM se ejecuta como usuario no root (`PUID`/`PGID`) y es propietario de ese directorio, el instalador, que no tiene capabilities, no puede escribir allí y lo indica. No puede recargar NPM: NPM aplica el archivo en su siguiente recarga, que ocurre cuando guardas cualquier proxy host (crear el host del panel en el paso 4 basta) o reinicias NPM. Para comprobarlo o forzarlo ahora: `docker exec <npm> nginx -t && docker exec <npm> nginx -s reload`.

#### Manual

Instala el archivo, valida y recarga (ejecuta la línea opcional con `sed` entre la copia y el renombrado para la variante):

```sh
docker exec <npm> mkdir -p /data/nginx/custom
docker cp npm/server_proxy.conf <npm>:/data/nginx/custom/server_proxy.conf.new
# optional, only for the legitimate-503 policy:
docker exec <npm> sed -i 's/^error_page 502 503 504 =503 @backsoon;/error_page 502 504 =503 @backsoon;/' /data/nginx/custom/server_proxy.conf.new
docker exec <npm> mv /data/nginx/custom/server_proxy.conf.new /data/nginx/custom/server_proxy.conf
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

- `mkdir -p` es necesario en un NPM recién instalado, donde `/data/nginx/custom` todavía no existe.
- Copiar a `.new` y renombrar hace que el archivo aparezca de forma atómica: NPM solo incluye el nombre exacto `server_proxy.conf`.
- NPM incluye el archivo dentro de cada proxy host, así que **sin proxy hosts todavía** `nginx -t` no lo ejercita. Repite `nginx -t && nginx -s reload` después de crear el primer host en el paso 4.
- Una recarga es asíncrona: los workers antiguos terminan primero sus peticiones. Espera unos segundos antes de probar.
- Si `docker cp` se rechaza (`Could not find the file`), tu `/data` es un `tmpfs`; usa un volumen o un bind mount.

No pongas nada en la pestaña *Advanced* de los proxy hosts: el archivo global es la única configuración que necesita backsoon. [Cómo funciona y qué pasa cuando `backsoon` no está disponible](../README.es.md#configuración-global-de-npm).

### Paso 4: publica el panel, inicia sesión y crea páginas

1. En NPM, añade un **proxy host** con estos valores (las etiquetas pueden variar ligeramente entre versiones de NPM). Reenvía por nombre de contenedor a través de la red compartida, como se indica abajo, y no al puerto publicado del panel en el host: el fallback necesita esa red de todos modos (`backsoon:80`), y a través del puerto del host la dirección de NPM queda fuera de `BACKSOON_TRUSTED_PROXIES`, de modo que todos los visitantes compartirían un único contador del límite de inicios de sesión:

   | Campo | Valor |
   |-------|-------|
   | Domain | `backsoon.example.com` (el tuyo) |
   | Scheme | `http` |
   | Forward hostname | `backsoon-admin` |
   | Forward port | `8080` |
   | SSL | solicita o selecciona un certificado y fuerza SSL |
   | Advanced | vacío |

   La cookie de sesión del panel es `Secure`, así que el inicio de sesión solo funciona por HTTPS (o en `http://localhost`, por ejemplo a través de un túnel SSH al `127.0.0.1:8181` publicado). Ten en cuenta la Access List de NPM o un host accesible solo desde la LAN o una VPN como capa adicional.
2. Repite `nginx -t && nginx -s reload` en NPM si este es tu primer proxy host (consulta el paso 3).
3. Abre `https://backsoon.example.com` e inicia sesión con `BACKSOON_ADMIN_USER` y la contraseña que convertiste en hash.
4. Crea una página a partir de la plantilla (o sube tu propio `.html`) y asígnale sus **destinos**: el dominio de la aplicación (`app1.example.com`) o el `service:port` al que reenvía NPM (`app1:3000`). Gana la primera coincidencia en este orden: dominio, después `service:port`, después la página predeterminada. Los cambios se aplican en la siguiente petición, sin reiniciar.

### Verificación

Con la aplicación detenida (o usa la variante segura de abajo):

```sh
curl -sI https://app1.example.com
# HTTP/… 503, Retry-After: 10, Cache-Control: no-store, X-Backsoon: 1 (each exactly once)
curl -sI -X POST https://app1.example.com                          # 503, not 405
curl -sI -H 'Accept: application/json' https://app1.example.com    # Content-Type: application/json
curl -s  -H 'Accept: application/json' https://app1.example.com    # {"status":"maintenance","retry_after":10}
```

Si todavía no tienes DNS, añade `-H 'Host: app1.example.com'` y apunta curl a la dirección de NPM por HTTP sin cifrar.

**Variante segura, sin detener una aplicación real.** Añade un proxy host temporal `test.example.com` que reenvíe a `backsoon:9` (un puerto cerrado): la conexión es rechazada y la petición debe recibir la página de mantenimiento `503`. Elimina el host después.

**NPM se reinicia con `backsoon` detenido.** `docker stop backsoon && docker restart <npm>`: todas las aplicaciones sanas deben seguir respondiendo. Vuelve a iniciar `backsoon` después.

**Cuenta las páginas de mantenimiento servidas.** NPM registra el fallback controlado como `<upstream status> : 503` en el campo upstream:

```sh
docker exec <npm> sh -c "grep -hE ' ([0-9]{3}|-)( :|,) 503 503 ' /data/logs/proxy-host-*_access.log"
```

Una conexión rechazada por la aplicación registra `502 : 503`. Un contenedor detenido que ya no se resuelve en el DNS de Docker registra `-, 503`. Una aplicación que devuelve su propio `503` (política por defecto) registra `503 : 503`; con la variante `502 504` registra `503 503` a secas y no coincide. Esto no es el `"502, 503"` del runbook del PRD (§13, RNF-9): NPM 2.16 imprime `502 : 503`, así que un grep de la cadena entre comillas no encuentra nada.

### Cuando `backsoon` o el panel está detenido

| Detenido | Resultado |
|----------|-----------|
| `backsoon` | NPM arranca, recarga y sirve todas las aplicaciones sanas. Una aplicación detenida recibe el `502` genérico propio de NPM (sin cabeceras ni cuerpo de mantenimiento), no un bloqueo. |
| `backsoon-admin` (el panel) | `backsoon` sigue sirviendo todas las páginas. El dominio propio del panel responde con la página de mantenimiento predeterminada (`503`) hasta que vuelva. |
| una aplicación | Su dominio muestra su página (o la predeterminada), `503`. Para esto sirve backsoon. |

### Reversión

Desactiva el archivo global (no cambia nada más: ningún proxy host, ninguna página almacenada). Con la instalación automática, primero quita `BACKSOON_NPM_CUSTOM_DIR` del stack y vuelve a desplegar; de lo contrario, el siguiente despliegue instala el archivo otra vez y NPM lo recoge en su siguiente recarga. Después:

```sh
docker exec <npm> mv /data/nginx/custom/server_proxy.conf /data/nginx/custom/server_proxy.conf.off
docker exec <npm> nginx -t && docker exec <npm> nginx -s reload
```

NPM incluye el archivo de forma opcional, así que la recarga no falla. Los contenedores de `backsoon` pueden seguir en marcha o detenerse; no tienen efecto sin el archivo. Restaura con `docker exec <npm> mv /data/nginx/custom/server_proxy.conf.off /data/nginx/custom/server_proxy.conf` y el mismo `nginx -t && nginx -s reload`. Para eliminar el stack, bórralo en Portainer o ejecuta `docker compose down`; añade `-v` solo si también quieres borrar las páginas y los datos del panel (`backsoon_pages`, `backsoon_meta` y el vacío `backsoon_npm_custom`). Eliminar el stack no elimina un `server_proxy.conf` instalado automáticamente: desactívalo como se indica arriba.

### Después de actualizar NPM

```sh
docker exec <npm> nginx -t
```

Después repite las comprobaciones de [Verificación](#verificación) (la página de mantenimiento con una aplicación detenida, y NPM arrancando con `backsoon` detenido), o ejecuta `BACKSOON_TEST_NPM_IMAGE=jc21/nginx-proxy-manager:<version> make e2e` contra la nueva versión (consulta [la guía del arnés](npm-harness.md), en inglés).

### Actualizar backsoon

Trae los cambios del repositorio y vuelve a desplegar reconstruyendo: `docker compose up -d --build`, o vuelve a desplegar el stack Git en Portainer para que descargue de nuevo el repositorio y reconstruya (los nombres de las opciones de Portainer no están verificados). Las páginas existentes se conservan: la siembra nunca sobrescribe `default.html`, y el panel vuelve a publicar sus páginas desde `backsoon_meta` al arrancar.

### Qué se ha verificado

Un ensayo en host limpio de estos cuatro pasos, con una red aislada y un NPM `2.16.0` de pruebas, se superó: construcción, entrega de `backsoon-init`, las comprobaciones anteriores, la instalación de `server_proxy.conf`, reversión y restauración, la variante `502 504`, inicio de sesión en el panel y creación de páginas a través de NPM, y CA-1 con una aplicación detenida. El ensayo no cubrió:

- **TLS.** No se emitió ningún certificado; se accedió al panel por HTTP enviando a mano la cookie `Secure`.
- **Las interfaces reales de NPM y Portainer.** Los proxy hosts se crearon mediante la API de NPM y no se usó Portainer; los pasos de Portainer siguen el comportamiento de Compose, no una ejecución en Portainer.
- **Producción.** Aplicaciones reales, redespliegues reales y la medición manual en el navegador del tiempo de recuperación (CA-2, CA-7).

El despliegue en producción de abajo cubre los dos primeros puntos y parte del tercero.

#### Producción (U8)

En el servidor del propietario, desplegado como stack Git de Portainer desde `main` (`b706071`) con la instalación automática:

- **Host.** Ubuntu 24.04 en `aarch64` (Oracle Ampere), Docker 29.8, Portainer EE, NPM `2.15.1` con su propia red `npm-nw` (`BACKSOON_PROXY_NETWORK=npm-nw`, `BACKSOON_TRUSTED_PROXIES` definida con el rango de esa red). Sitios detrás de Cloudflare.
- **Despliegue y redespliegue.** Ambas imágenes se construyeron en el host; *Pull and redeploy* las reconstruyó; `backsoon-init` y `backsoon-npm-snippet` terminaron con código 0 y el log del snippet dijo `installed`; `backsoon` y `backsoon-admin` estaban sanos, con el panel publicado solo en `127.0.0.1:8181`.
- **NPM.** El snippet se activó en la siguiente recarga de NPM (al guardar el proxy host del panel): las respuestas llevaban `X-Backsoon: 1`. NPM lo incluye en cada proxy host, existente y nuevo.
- **Panel.** Se accedió a través de un proxy host de NPM a `backsoon-admin:8080` con un certificado de Let's Encrypt y Force SSL; el inicio de sesión y la creación de páginas funcionaron.
- **Aplicaciones.** Una aplicación real detenida detrás de NPM mostró su página de mantenimiento, y el propietario confirmó que funcionaba.

No medido: el tiempo de recuperación en el navegador después de que una aplicación vuelve (CA-2).

### Diferencias con el runbook del PRD (§13)

- El comando de comprobación usa `127.0.0.1`, no `localhost`, y el `curl` del lado de NPM muestra las cabeceras.
- `mkdir -p /data/nginx/custom` es necesario en un NPM recién instalado.
- El hash del panel usa bcrypt con coste 12 y la imagen fijada `httpd:2.4-alpine`, como en el README.
- La traza del log es `502 : 503` (o `-, 503`), no `"502, 503"`.
