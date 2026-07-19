# Tokens de sesión

Cómo el servidor verifica que una reconexión (`join_room` con un `playerId`
ya conocido) es realmente ese jugador y no otra conexión que adivinó o
interceptó su ID.

## Modelo de confianza

No hay cuentas de usuario, contraseñas ni JWT firmado — cada **sala**
(`internal/room.Room`) emite un secreto opaco por jugador, en memoria, que
vive tanto como la sala misma. Es proporcional al resto del diseño: salas
efímeras, un proceso, sin persistencia entre partidas.

**Primero en reclamar, gana:** la primera conexión física que manda
`join_room` para un `playerId` que todavía no tiene token asignado en esa
sala lo reclama — se le genera un token y se registra como dueño de esa
identidad para el resto de la vida de la sala. Cualquier `join_room`
posterior para ese mismo `playerId` (reconexión) tiene que traer ese token
exacto o se rechaza; la conexión que lo intentó sin éxito **no** reemplaza
al jugador legítimo.

Esto no impide que alguien reclame un `playerId` ajeno *antes* que el
jugador real llegue a conectarse por primera vez — mitigarlo requeriría
autenticación real (cuentas, backend de identidad), fuera de alcance para
un backend de salas efímeras. Lo que sí cierra es el caso real y más
probable: alguien secuestrando la identidad de un jugador **después** de
que ya estableció su lugar en la sala (p. ej. durante el grace period de
reconexión de una partida en curso).

## Contrato de wire

### `session_token` (servidor → cliente, dirigido)

Se manda **una sola vez**, únicamente a la conexión que acaba de reclamar
un `playerId` sin token previo — nunca por broadcast, nunca a otros
jugadores de la sala.

```json
{ "type": "session_token", "token": "s3cr3t-opaco-base64url" }
```

El cliente debe guardarlo (en memoria, junto al resto del estado de la
conexión — no hace falta persistirlo entre reinicios de la app, el
alcance es el mismo que el de la reconexión con grace period) y mandarlo de
vuelta en cualquier `join_room` posterior para el mismo `playerId`.

### `join_room` (cliente → servidor) — campo `token`

```json
{ "type": "join_room", "playerId": "p1", "name": "Ana", "token": "s3cr3t-opaco-base64url" }
```

- **Primer join de un `playerId` nuevo en la sala:** `token` se omite (o
  va vacío) — no hay nada que validar todavía, el servidor emite uno nuevo.
- **Reconexión** (`playerId` ya tiene un token emitido en esa sala):
  `token` es **obligatorio** y debe matchear exactamente. Si falta o no
  coincide, el servidor responde `ws_error` con
  `{"message": "Token de sesión inválido"}` y esa conexión **no** se
  registra como el jugador — no hay período de gracia para clientes que
  todavía no implementan tokens.

## Por qué no se mandan en `room_state`

`room_state` (`LobbySnapshot`) se transmite igual a todos los jugadores de
la sala — es información pública del lobby (quién está, quién es host, si
está listo). Un secreto de sesión ahí se filtraría a todos los demás
jugadores. Por eso `session_token` es un mensaje aparte, dirigido solo a la
conexión dueña del secreto.
