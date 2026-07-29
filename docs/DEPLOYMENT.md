# Puesta en marcha: Supabase + despliegue

Guía paso a paso para levantar tu propia instancia — Supabase (auth +
persistencia) es opcional pero recomendado; sin configurarlo el servidor
funciona igual, 100% invitado y sin historial. Todo acá son variables de
entorno: cualquiera que se clone el repo puede montar su propio Supabase y
su propio despliegue siguiendo los mismos pasos, sin tocar código.

## 1. Crear el proyecto en Supabase

1. [supabase.com](https://supabase.com) → **New Project**. Elegí una
   contraseña de base de datos fuerte y guardala — es lo único que no
   podés volver a ver después (se puede resetear en *Project Settings →
   Database*, pero no recuperar).
2. Esperá a que termine de aprovisionar (unos minutos).

## 2. Aplicar el esquema

Abrí **SQL Editor** en el panel del proyecto, pegá el contenido completo de
[`supabase/migrations/0001_init.sql`](../supabase/migrations/0001_init.sql)
y ejecutalo. Crea las cuatro tablas (`players`, `matches`, `match_players`,
`audit_events`) con RLS habilitado.

Si más adelante agregamos más migraciones y preferís no copiar/pegar cada
vez, se puede usar el [CLI de Supabase](https://supabase.com/docs/guides/cli)
(`supabase link --project-ref <ref>` + `supabase db push`) — para el
arranque inicial, el SQL Editor alcanza y es más simple.

## 3. Habilitar Anonymous Sign-Ins

*Project Settings → Authentication → User Signups* → activá **Allow
anonymous sign-ins**. Sin esto, un jugador sin cuenta real no puede obtener
un `authToken` — solo podría jugar como invitado puro (sin identidad
persistente ni historial).

## 4. De dónde salen las dos variables que necesita el servidor

El server **no usa las API keys de Supabase** (`anon`/`service_role`) para
nada — se conecta directo a Postgres con `pgx` y valida JWTs offline contra
el JWKS público. Solo necesitás dos cosas:

### `SUPABASE_JWKS_URL`

Es pública, no es un secreto. Se arma así:

```
https://<project-ref>.supabase.co/auth/v1/.well-known/jwks.json
```

El `project-ref` está en la URL del dashboard de tu proyecto, o en
*Project Settings → General → Reference ID*.

### `DATABASE_URL`

Botón **Connect** (arriba del dashboard) → pestaña **Session pooler**
(no "Direct connection": esa solo resuelve por IPv6, y la mayoría de
plataformas — Cloud Run, la mayoría de VMs, contenedores en redes
IPv4-only en general — no tienen salida IPv6 por default, así que la
conexión directa falla con "network is unreachable". El *Session pooler*
sí soporta IPv4 y, a diferencia del *Transaction pooler*, se comporta
como una conexión normal de sesión — que es lo que este server necesita,
un único proceso de vida larga). Copiá la URI y reemplazá
`[YOUR-PASSWORD]` por la contraseña del paso 1 — notá que el usuario acá
lleva el project-ref pegado (`postgres.<project-ref>`, no `postgres` a
secas):

```
postgresql://postgres.<project-ref>:<tu-password>@aws-0-<region>.pooler.supabase.com:5432/postgres
```

Si tu red sí tiene salida IPv6 (poco común en la práctica), la conexión
directa (`db.<project-ref>.supabase.co:5432`) también funciona y evita
depender del pooler — pero como no es el caso común, el pooler de sesión
es la opción segura por default.

## 5. Correr el servidor con esas variables

Local, sin Docker:

```bash
export SUPABASE_JWKS_URL="https://<project-ref>.supabase.co/auth/v1/.well-known/jwks.json"
export DATABASE_URL="postgresql://postgres:<password>@db.<project-ref>.supabase.co:5432/postgres"
go run ./cmd/server
```

Con Docker, construyendo la imagen local:

```bash
docker build -f deploy/Dockerfile -t cards_game_service:local .
docker run --rm -p 8080:8080 \
  -e SUPABASE_JWKS_URL="https://<project-ref>.supabase.co/auth/v1/.well-known/jwks.json" \
  -e DATABASE_URL="postgresql://postgres:<password>@db.<project-ref>.supabase.co:5432/postgres" \
  cards_game_service:local
```

### Cómo saber si quedó bien conectado

```bash
curl localhost:8080/healthz          # 200 siempre, no dice nada de Supabase
curl localhost:8080/leaderboard      # [] (vacío, JSON) si Supabase está OK
                                      # 503 "persistencia no configurada" si NO se están leyendo las env vars
```

Si cualquiera de las dos está mal (typo, proyecto pausado, contraseña
vieja), el proceso **no arranca**: `cmd/server/main.go` valida ambas al
inicio y falla rápido con el error en los logs — mejor eso que arrancar a
medias con la auth o la persistencia rotas en silencio. Si directamente no
configurás ninguna de las dos (las dejás vacías), el server arranca normal
en modo 100% invitado.

### Conseguir un `authToken` para probar `join_room` a mano

El camino real es el cliente Flutter llamando
`supabase.auth.signInAnonymously()`, que devuelve una sesión con
`access_token` (ese es el JWT). Para probar sin el cliente, con curl —
necesitás la `anon key` acá sí (es pública, está pensada para usarse desde
cualquier cliente; *Project Settings → API Keys*):

```bash
curl -X POST 'https://<project-ref>.supabase.co/auth/v1/signup' \
  -H "apikey: <tu-anon-key>" \
  -H "Content-Type: application/json" \
  -d '{}'
```

La respuesta trae `access_token` — ese string es el `authToken` que va en
`join_room` (ver [`docs/PERSISTENCE.md`](PERSISTENCE.md)).

## 6. Publicar en la plataforma que elijas

Para que otros se puedan conectar hace falta algo corriendo 24/7 en
internet, no solo local. El proyecto no está atado a ninguna plataforma
en particular — lo único que necesita cualquier host es:

- Correr un contenedor Docker (`deploy/Dockerfile`) de forma **continua**,
  no solo cuando llega tráfico. Los timers de reconexión y de
  `LobbyIdleTimeout` (`internal/room/timers.go`) son `time.AfterFunc`
  reales — necesitan el proceso corriendo para disparar, no alcanza con
  que responda cuando llega una request. Varias plataformas serverless
  "suspenden" instancias sin tráfico por default (Cloud Run, Fly.io
  incluidos) — hay que revisar explícitamente la opción de esa
  plataforma para mantener una instancia siempre activa.
- **Una sola instancia** a la vez (`min = max = 1` o equivalente): el
  estado de cada sala vive en memoria de un único proceso, no hay
  coordinación entre instancias (ver `docs/ARCHITECTURE.md#despliegue`).
- Soportar WebSockets de larga duración (algunas plataformas cortan la
  conexión a los N minutos aunque siga activa — no es fatal gracias al
  token de sesión y el grace period de reconexión, pero conviene
  configurar el timeout más alto que la plataforma permita).
- Pasarle `SUPABASE_JWKS_URL` y `DATABASE_URL` como variables de entorno
  (o secretos, para `DATABASE_URL` — trae una contraseña).

Ejemplos de plataformas donde esto encaja: Fly.io (Machines con
`min_machines_running`/`auto_stop_machines`), Google Cloud Run (con CPU
siempre asignada e instancia mínima en 1), Oracle Cloud "Always Free", o
cualquier VM propia corriendo el contenedor con Docker/systemd. La
configuración exacta (cómo se pasan env vars, cómo se fija una instancia
siempre viva, cómo se expone HTTPS) depende de la plataforma que elijas —
consultá su documentación para esa parte.

Nada de lo anterior es específico a ninguna cuenta: cualquiera que se
clone el repo puede repetir los pasos 1-5 con su propio proyecto de
Supabase y publicar en la plataforma que prefiera — son todo variables de
entorno, no hay nada hardcodeado.
