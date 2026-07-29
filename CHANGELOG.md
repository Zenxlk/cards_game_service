# Changelog

Formato basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/),
versionado según [Semantic Versioning](https://semver.org/lang/es/).

## [Unreleased]

Motor de reglas de Exploding Kittens: casos límite adicionales.

## [0.3.0] - 2026-07-29

### Agregado

- Identidad de jugador persistente entre partidas vía Supabase Auth
  (cuenta real o Anonymous Sign-In), validada por JWT contra el JWKS del
  proyecto (`authToken` opcional en `join_room`). Sin Supabase
  configurado, el servidor sigue siendo 100% invitado, igual que antes.
  Detalle completo en [`docs/PERSISTENCE.md`](docs/PERSISTENCE.md).
- Persistencia asíncrona en Postgres (Supabase) de partidas, participantes
  autenticados y eventos de auditoría de seguridad/lobby — nunca bloquea
  el goroutine de una sala; si la escritura falla o la cola se llena, se
  descarta y se loguea en vez de afectar la partida.
- Endpoints `GET /players/{id}`, `GET /leaderboard` y
  `PATCH /players/{id}/nickname`.
- Configuración nueva, ambas opcionales: `SUPABASE_JWKS_URL` y
  `DATABASE_URL`.

### Cambiado

- Go 1.24 → 1.25 (requerido por `jackc/pgx/v5`), CI y `deploy/Dockerfile`
  actualizados.

### Corregido

- `closeAll()` (cierre de sala: panic recuperado, `LobbyIdleTimeout`, o el
  host se va) solo cerraba las conexiones ya unidas (`r.clients`) — las
  que habían abierto el WebSocket pero todavía no completaban `join_room`
  (`r.pending`) se quedaban sin ninguna señal, esperando una respuesta que
  nunca llegaba. Ahora también reciben un `ws_error` y su canal se cierra.
- Logging pasa a JSON con clave `severity` (`cmd/server/logging.go`): el
  handler de texto por default de `slog` no le llegaba con severidad
  reconocible a Cloud Logging, así que ninguna alerta basada en
  `severity>=ERROR` podía disparar por más que el proceso logueara bien
  el error.

## [0.2.0] - 2026-07-25

### Agregado

- Tokens de sesión para reconexión: la primera conexión que reclama un
  `playerId` recibe un `session_token`; reconectar exige ese token o se
  rechaza, evitando que otra conexión secuestre la identidad de un jugador
  a mitad de partida. Detalle del contrato en
  [`docs/TOKENS.md`](docs/TOKENS.md).
- `recover()` en el goroutine de cada sala (`Room.safeExec`): un panic en el
  motor de un juego (bug propio, no input malicioso necesariamente) cierra
  solo esa sala en vez de tirar todo el proceso con todas las partidas en
  curso.
- Salas en fase `waiting` (lobby, sin partida arrancada) se cierran solas
  tras `LobbyIdleTimeout` (10 minutos por defecto) — sin esto, `POST /rooms`
  sin uso real dejaba goroutines y memoria vivos para siempre.
- Límite de tamaño (4 KiB) en el body de `POST /rooms` y validación de
  longitud de `playerId`/`name` (`join_room` y `POST /rooms`).

### Corregido

- `onJoin` sacaba la conexión de `pending` antes de saber si el `join_room`
  se iba a aceptar — si se rechazaba (token inválido, sala llena, partida ya
  empezada), la conexión quedaba sin trackear en ningún lado, un limbo del
  que nada la limpiaba. Ahora solo se saca de `pending` cuando el join tiene
  éxito.
- `room_state` (`LobbySnapshot`) no incluía `maxPlayers` — el cliente
  Flutter (`LobbyRoom.fromJson`) lo espera como `int` no-nullable, así que
  el parseo fallaba en silencio contra el backend real.
- Migro de `nhooyr.io/websocket` a `github.com/coder/websocket`: el autor
  original transfirió el proyecto a Coder y marcó todo `nhooyr.io/websocket`
  como deprecado en favor de ese fork, que es quien lo sigue manteniendo.
  Mismo API, sin cambios de comportamiento — solo el import path y la
  entrada en `go.mod`.

## [0.1.1] - 2026-07-19

### Corregido

- Publicación de la imagen también en Docker Hub además de `ghcr.io`
  (`.github/workflows/publish-image.yml`).
- Nombre del módulo Go y todas las referencias (imports, CI, README,
  CHANGELOG) corregidas de `cards-game-service` a `cards_game_service`,
  igual que en GitHub y Docker Hub.

## [0.1.0] - 2026-07-18

Primera versión del servidor: estructura completa, compilando y probada de
punta a punta.

### Agregado

- `pkg/engine`: contrato `GameEngine` genérico (`Start`, `Apply`,
  `DecodeAction`, `View`, `PendingTimer`, transiciones de
  desconexión/reconexión) + registro de motores estilo `database/sql`.
- `games/explodingkittens`: puerto fiel de las reglas de Exploding Kittens
  desde el cliente Flutter existente (mazo, turnos, ventana de Nope, Defuse,
  Favor, pares/tríos de gato, condición de victoria), con ocultamiento de
  información del lado servidor que la versión LAN original no tenía.
- `games/fixture`: motor mínimo sin información oculta, usado solo por los
  tests de infraestructura.
- `internal/room`: nodo de partida — un goroutine por sala, máquina de
  estados `waiting → active → finished`, timers de reconexión y de ventana de
  reacción.
- `internal/lobby`: registro de salas por código.
- `internal/transport`: servidor WebSocket (`nhooyr.io/websocket`) con el
  mismo formato de wire que ya habla el cliente Flutter (`join_room`,
  `action`, `game_state`, `game_event`, etc.).
- `cmd/server`: wiring del binario; `deploy/Dockerfile` multi-stage sobre
  imagen `distroless`.
- Tests unitarios del motor (RNG determinista, sin mocks) y un test de
  integración con servidor HTTP real + clientes WebSocket reales.

[Unreleased]: https://github.com/ZenXLK/cards_game_service/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/ZenXLK/cards_game_service/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ZenXLK/cards_game_service/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/ZenXLK/cards_game_service/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ZenXLK/cards_game_service/releases/tag/v0.1.0
