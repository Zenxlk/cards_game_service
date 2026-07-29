# Persistencia e identidad entre partidas

Cómo el servidor guarda identidad de jugadores, historial de partidas y
auditoría de seguridad/lobby — todo opcional: sin configurar Supabase, el
servidor funciona exactamente igual que antes (100% invitado, sin
historial ni tops).

## Modelo de identidad

Además del token de sesión por sala (`docs/TOKENS.md`, que resuelve
reconexión *dentro* de una partida), esta capa agrega identidad
*persistente entre partidas* vía Supabase Auth — cuenta real o **Anonymous
Sign-In** (Supabase emite un JWT real con `is_anonymous: true`, respaldado
por una fila en `auth.users` que se puede vincular después a una cuenta
permanente sin perder historial). Ambos casos comparten el mismo camino de
verificación: no hay una noción especial de "invitado con cuenta" separada
de "cuenta real", solo `is_anonymous`.

Un jugador sin ningún JWT (invitado puro, sin siquiera Anonymous Sign-In)
sigue pudiendo jugar exactamente como antes — simplemente no queda
identidad persistente ni aparece en historial/tops.

## Contrato de wire: `authToken` en `join_room`

```json
{ "type": "join_room", "playerId": "...", "name": "Ana", "token": "...", "authToken": "<jwt-supabase>" }
```

- `authToken` es opcional. Si viene vacío, el join se comporta exactamente
  como sin esta feature (invitado, `playerId` es lo que mande el cliente).
- Si viene no vacío, el servidor lo valida contra el JWKS del proyecto
  Supabase (`internal/auth`, offline vía JWKS, sin round-trip a Supabase
  por cada join). Si es inválido (firma, expiración), el join se
  **rechaza entero** — no degrada en silencio a invitado.
- Si es válido, el `playerId` de esa conexión pasa a ser el `sub` del
  JWT — se ignora lo que haya mandado el cliente en el campo `playerId`.
  Esto es a propósito: nadie puede reclamar la identidad autenticada de
  otro jugador sin tener su JWT.

**Importante para el cliente:** si un jugador va a autenticarse, su
`playerId` en `join_room` y su `hostId` en `POST /rooms` (si es el que
crea la sala) deben ser igualmente su `sub` de Supabase desde el
principio — `authToken` solo lo *verifica*, no sustituye ese contrato. Si
el host crea la sala con un `hostId` que no coincide con su `sub`, al
unirse autenticado terminará registrado como un jugador nuevo en vez de
como el host original.

## Esquema

Ver la migración completa en
[`supabase/migrations/0001_init.sql`](../supabase/migrations/0001_init.sql).
Resumen:

- **`players`**: espejo liviano de `auth.users` — un registro por
  identidad (anónima o real), con `nickname` editable y `is_anonymous`
  actualizado en cada `join_room` autenticado.
- **`matches`** / **`match_players`**: una fila por partida arrancada y
  sus participantes autenticados. Solo participantes con JWT válido
  quedan acá — un invitado sin identidad persistente no puede aparecer
  (violaría la FK a `players(id)` a propósito).
- **`audit_events`**: eventos de seguridad/lobby (join rechazado, sala
  creada, panic recuperado en el motor de un juego, etc.) — **no**
  acciones de partida jugada por jugada.

Las cuatro tablas tienen `row level security` habilitado sin ninguna
policy: el backend siempre lee y escribe con la `service_role key`
(bypassea RLS por diseño), así que si alguna vez se filtra la `anon key`
del proyecto, estas tablas quedan con cero acceso vía esa key.

## Escritura asíncrona

`internal/store.Store` nunca bloquea al goroutine de una `Room`: las
escrituras (`UpsertPlayer`, `RecordMatchStart`, `RecordMatchEnd`,
`RecordAuditEvent`) se encolan en un canal con buffer y las procesa un
worker aparte. Si la cola se llena (Postgres lento o caído), el registro
se descarta y se loguea — es una garantía *best-effort*: perder una fila
de auditoría o de historial es aceptable, bloquear una partida por un
problema de base de datos no. Las lecturas (`GetPlayerProfile`,
`GetLeaderboard`, usadas por los endpoints HTTP) son síncronas — no hay
razón para encolarlas, no corren en el goroutine de una sala.

## Cierre de partida: `finished` vs `abandoned`

`matches.status` queda en `finished` cuando el motor del juego reporta el
final por sus propias reglas (`engine.Terminal`), y en `abandoned` cuando
la sala se cierra por un panic recuperado del motor a mitad de partida
(`Room.crashClose`, ver la sección de robustez en `docs/ARCHITECTURE.md`).
Nunca se escribe dos veces el cierre de la misma partida.

## Endpoints HTTP

- `GET /players/{id}` — perfil + estadísticas (partidas jugadas,
  victorias). 404 si no existe, 503 si no hay persistencia configurada.
- `GET /leaderboard?limit=N` — top jugadores por victorias (`limit`
  default 20, máximo 100). Consulta agregada en vivo, no contadores
  materializados — con el tráfico esperado no hace falta y evita que se
  desincronicen de `match_players`.
- `PATCH /players/{id}/nickname` — requiere `Authorization: Bearer
  <jwt-supabase>` cuyo `sub` coincida exactamente con `{id}`; nadie puede
  cambiar el nickname de otro jugador.

## Configuración

- `SUPABASE_JWKS_URL`: URL del JWK Set del proyecto
  (`https://<project_ref>.supabase.co/auth/v1/.well-known/jwks.json`).
  Vacío (default) = sin Supabase, servidor 100% invitado.
- `DATABASE_URL`: connection string de Postgres (el de Supabase, o
  cualquier otro). Vacío (default) = sin persistencia.

Ambas son independientes: se puede tener JWKS sin `DATABASE_URL` (valida
identidad pero no persiste nada) o viceversa, aunque en la práctica no
tiene mucho sentido usar una sin la otra.
