# Arquitectura

Este documento explica las decisiones de diseño que no son obvias mirando
solo el código — el *por qué*, no el *qué* (para el qué, mejor leer el código
mismo, está comentado en español).

## El contrato: `pkg/engine.GameEngine`

```go
type GameEngine interface {
    Start(players []PlayerInfo, config json.RawMessage) (State, error)
    DecodeAction(actor PlayerID, raw json.RawMessage) (Action, error)
    Apply(state State, action Action) (State, []Event, error)

    ResolveNopeWindow(state State) (State, []Event, error)
    MarkPlayerDisconnected(state State, player PlayerID) (State, []Event, error)
    MarkPlayerReconnected(state State, player PlayerID) (State, []Event, error)
    EliminateForDisconnect(state State, player PlayerID) (State, []Event, error)

    View(state State, viewer PlayerID) (any, error)
    PendingTimer(state State) (d time.Duration, ok bool)
}
```

Puntos que no son evidentes a primera vista:

- **`Apply` es puro.** Sin I/O, sin goroutines propias, sin estado interno del
  motor — el estado siempre entra y sale explícito. Eso es lo que hace que
  `room` pueda correr cualquier juego en su propio goroutine sin coordinarse
  con nada más, y que los tests de motor no necesiten levantar servidor ni
  mocks (ver `games/explodingkittens/engine_test.go`).

- **`ResolveNopeWindow`/`MarkPlayerDisconnected`/etc. están separados de
  `Apply` a propósito.** No son acciones de un jugador jugando su turno —
  las dispara un timer del `room` (una ventana de reacción que expiró, un
  grace period de reconexión agotado) y por eso no pasan por la misma
  validación que una acción real. Mezclarlos con `Apply` obligaría a fingir
  una acción de jugador para algo que no lo es.

- **`PendingTimer` es la pieza que mantiene a `room` genérico.** Un juego
  como Exploding Kittens tiene una ventana de tiempo para jugar "Nope"
  después de que alguien juega una carta — pero `room` no sabe qué es una
  "ventana de Nope", ni debería. En cada transición, el motor le dice al
  room "necesito un timer de N milisegundos, y si expira llamá a
  `ResolveNopeWindow`". El room solo obedece. Si mañana otro juego necesita
  un tipo de timer distinto, esa parte crece; no hizo falta inventar un
  mecanismo genérico de timers arbitrarios que nadie más usa hoy.

- **`View` es la única cara pública del estado.** `State` puede (y en
  Exploding Kittens, tiene) información que un jugador concreto no debería
  ver — la mano del resto, el orden del mazo, qué cartas mostró "Ver el
  futuro". `room` nunca transmite `State` crudo; siempre pasa por `View(state,
  viewer)`, que decide qué proyectar para cada destinatario.

## Por qué `lobby` es tan chico

La lógica de "unirse a la sala / marcarse listo / salir / arrancar la
partida" vive **dentro de `room.Room`**, no en `lobby`. `lobby` es solo un
registro: genera un código, mapea código → `*room.Room`, y lo saca del mapa
cuando la sala termina.

La razón es que esa lógica de lobby necesita el mismo dueño exclusivo de
estado que la partida en sí — es la misma máquina de estados
(`waiting → active → finished`), solo que `waiting` todavía no tiene motor de
juego corriendo. Partirla en dos objetos distintos (uno para "antes de
arrancar" y otro para "durante la partida") hubiera significado coordinar dos
dueños de estado en vez de uno, sin ninguna ganancia real.

## Reconexión

Cada sala lleva un timer de grace period por jugador desconectado
(`internal/room/timers.go`): si el jugador no vuelve a mandar `join_room`
antes de que expire, el `room` llama a `GameEngine.EliminateForDisconnect`.
Si vuelve a tiempo, se cancela el timer y se llama a
`MarkPlayerReconnected`. Todo esto corre en el mismo goroutine dueño de la
sala — el timer dispara en su propio goroutine (así es `time.AfterFunc` en
Go), pero lo primero que hace es encolar un comando de vuelta al canal de la
sala, nunca toca el estado directamente.

Del lado del cliente, la reconexión es responsabilidad suya: reintentar la
conexión WebSocket con backoff exponencial y volver a mandar `join_room` con
el mismo `playerId`.

`join_room` con un `playerId` ya conocido no se acepta a ciegas: la
primera conexión que reclama un `playerId` recibe un token de sesión
opaco (`session_token`, dirigido, nunca por broadcast); cualquier
`join_room` posterior para ese mismo `playerId` tiene que traer ese token
o se rechaza, sin reemplazar al jugador legítimo. Detalle completo del
contrato y del modelo de confianza ("primero en reclamar, gana") en
[`docs/TOKENS.md`](TOKENS.md).

## Identidad y persistencia

El token de sesión de arriba resuelve reconexión *dentro* de una sala.
Por encima de eso, `join_room` acepta un `authToken` opcional (JWT de
Supabase, validado contra su JWKS) que le da a un jugador una identidad
*persistente entre partidas* — real o anónima (Supabase Anonymous
Sign-In) — y habilita historial de partidas y tops en Postgres, con
escritura siempre asíncrona respecto al goroutine de la sala. Ambas
piezas (auth y persistencia) son opcionales: sin configurarlas, el
servidor funciona igual que antes, 100% invitado y sin guardar nada.
Contrato completo en [`docs/PERSISTENCE.md`](PERSISTENCE.md).

## Información oculta

Un juego de cartas con manos privadas necesita que el servidor sea la
autoridad sobre qué ve cada jugador — no alcanza con que el cliente decida no
*mostrar* algo que igual le llegó por la red, porque cualquiera puede leer el
tráfico o modificar el cliente. Por eso:

- `View(state, viewer)` proyecta manos ajenas a solo su cantidad de cartas,
  nunca su contenido.
- El orden y contenido del mazo nunca viaja completo — solo su tamaño y, si
  corresponde, la carta de arriba del descarte.
- Los eventos (`engine.Event`) declaran explícitamente sus destinatarios
  (`Recipients`) cuando exponen información que no todos deberían ver — por
  ejemplo, las cartas que reveló "Ver el futuro" solo van a quien jugó esa
  carta.

## Robustez del goroutine de sala

Dos garantías que no son obvias mirando `internal/room/room.go` en diagonal:

- **Un panic en el motor de un juego no tira el servidor.** `Room.Run`
  ejecuta cada comando encolado a través de `safeExec`, que envuelve la
  llamada en un `recover()`. Sin esto, un bug en un motor concreto (índice
  fuera de rango, mapa nil, lo que sea) mataría el proceso entero — Go no
  aísla goroutines — con todas las salas activas del servidor, no solo la
  afectada. Con `safeExec`, se pierde únicamente la sala donde ocurrió: se
  avisa a los clientes (`player_kicked`) y se cierra.
- **Una sala en lobby (`waiting`) no vive para siempre.** `LobbyIdleTimeout`
  (`internal/room.Config`, 10 minutos por defecto en producción) cierra
  automáticamente cualquier sala que nunca llega a arrancar partida — tapa
  el caso de `POST /rooms` sin uso real, que de otro modo deja goroutines y
  memoria vivos indefinidamente (nada más marca esa sala como `finished`).
  El timer se cancela apenas la partida arranca (`onStartGame`); a partir
  de ahí el ciclo de vida pasa a regirse por el grace period de reconexión.

## Despliegue

El estado de cada sala vive en memoria de un único proceso — no hay base de
datos ni estado compartido entre instancias. Eso hace que escalar
horizontalmente no sea gratis: si dos instancias del servidor corrieran en
paralelo, las conexiones de una misma sala tendrían que llegar siempre a la
instancia que la creó, y ninguna plataforma serverless lo garantiza por
default (la session affinity, cuando existe, suele ser por cliente, no
por sala, y es best-effort).

La decisión para arrancar: **una sola instancia siempre viva**. Con una
sola instancia el problema de afinidad no existe — todas las salas viven
en el mismo proceso. Es la opción más simple y más barata para un
proyecto que todavía no necesita escalar.

"Siempre viva" importa más de lo que parece: los timers de
`internal/room/timers.go` (grace period de reconexión, `LobbyIdleTimeout`,
ventana de Nope) son `time.AfterFunc` reales — necesitan que el proceso
esté corriendo para disparar, no alcanza con que responda cuando llega
tráfico. Muchas plataformas serverless suspenden instancias sin tráfico
por default (Cloud Run, Fly.io incluidos) — hay que desactivar
explícitamente esa suspensión, cada plataforma con su propio mecanismo.
Guía de configuración (Supabase + qué necesita cualquier plataforma) en
[`docs/DEPLOYMENT.md`](DEPLOYMENT.md).

Si en algún momento hace falta más de una instancia, el camino sin rediseñar
la aplicación es enrutar por código de sala con hashing consistente en una
capa de proxy/ingress, o externalizar el broadcast entre instancias vía
pub/sub (Redis, NATS). Ninguna de las dos hace falta hoy — se anota acá para
no tener que redescubrirlo cuando llegue el momento.
