<div align="center">

# cards_game_service

**Backend genérico en Go para juegos de cartas multijugador por WebSocket.**

[![CI](https://github.com/ZenXLK/cards_game_service/actions/workflows/ci.yml/badge.svg)](https://github.com/ZenXLK/cards_game_service/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/ZenXLK/cards_game_service.svg)](https://pkg.go.dev/github.com/ZenXLK/cards_game_service)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

---

## ¿Qué es esto?

Un servidor que orquesta **salas, transporte y reconexión** para juegos de cartas
multijugador — pero que **no conoce las reglas de ningún juego concreto**. Cada
juego es un *plugin* que implementa la interfaz [`engine.GameEngine`](pkg/engine/engine.go);
el servidor solo sabe:

- crear/listar/unir salas por código (`internal/lobby`),
- correr cada partida como un nodo aislado — un goroutine con su propio
  estado y canal de comandos (`internal/room`),
- hablar WebSocket con el cliente y serializar todo a JSON (`internal/transport`).

La implementación de referencia es un motor de **Exploding Kittens**
(`games/explodingkittens`) — un puerto fiel de las reglas de un cliente
Flutter existente, pensado para que ese cliente pueda pasar de jugar por LAN a
jugar online cambiando solo la URL de conexión, sin tocar su código.

## Arquitectura, en un vistazo

```
cliente WebSocket (Flutter, u otro)
        │
        ▼
internal/transport   ← HTTP/WS, formato de wire, no conoce reglas de juego
        │
        ▼
internal/lobby       ← código de sala → *room.Room
        │
        ▼
internal/room        ← un goroutine por partida: estado, timers, broadcast
        │
        ▼
pkg/engine            ← contrato GameEngine (genérico)
        ▲
        │ implementa
games/explodingkittens (o cualquier otro juego)
```

Cada juego concreto es una función pura: recibe un estado inmutable y una
acción, devuelve el estado siguiente más los eventos que produjo. Sin I/O, sin
goroutines propias — eso lo hace trivial de testear y es lo que le permite a
`room` correr cualquier juego sin conocer sus reglas.

Si querés el detalle de cómo se llegó a este diseño (por qué `lobby` es tan
chico, por qué las ventanas de reacción con timer viven en `room` y no en el
motor, qué información oculta un juego con manos privadas como Exploding
Kittens), está en [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Uso rápido

Requiere Go 1.24+.

```bash
go run ./cmd/server
# escuchando en :8080 (o $PORT si está seteada)
```

Crear una sala y jugar:

```bash
# 1. Crear una sala de Exploding Kittens
curl -X POST localhost:8080/rooms \
  -H 'Content-Type: application/json' \
  -d '{"gameType":"exploding_kittens","hostId":"p1","hostName":"Ana"}'
# -> {"code":"AB12CD"}

# 2. Conectar por WebSocket a ws://localhost:8080/ws/AB12CD
#    y mandar {"type":"join_room","playerId":"p1","name":"Ana"}
```

El formato completo de mensajes (`join_room`, `action`, `game_state`,
`game_event`, etc.) está documentado en
[`internal/transport/protocol.go`](internal/transport/protocol.go).

## Tests

```bash
go test ./...
```

Incluye tests unitarios del motor de Exploding Kittens con RNG determinista
(mismo seed → misma partida, sin mocks) y un test de integración que levanta
un servidor HTTP real y juega una partida completa con clientes WebSocket
reales (`test/integration/`).

## Agregar un juego nuevo

1. Creá un paquete en `games/<tu-juego>/` que implemente `engine.GameEngine`
   ([firma completa acá](pkg/engine/engine.go)).
2. Registralo en un `init()`: `engine.Register("tu-juego", NewEngine)`.
3. Importalo con blank import en `cmd/server/main.go` (`_ "github.com/ZenXLK/cards_game_service/games/tu-juego"`).

Nada más — `room`, `lobby` y `transport` no necesitan saber que tu juego
existe.

## Despliegue

Pensado para arrancar simple en Cloud Run: una sola instancia
(`min-instances=1 max-instances=1`), porque el estado de cada sala vive en
memoria de un único proceso. El `Dockerfile` en [`deploy/`](deploy/Dockerfile)
arma un binario estático sobre una imagen `distroless`. Cada Release publicado
en GitHub se construye y sube automáticamente a
`ghcr.io/zenxlk/cards_game_service` (ver
[`.github/workflows/publish-image.yml`](.github/workflows/publish-image.yml)).
Detalle de la
decisión (por qué un solo proceso, cómo escalar más adelante) en
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md#despliegue).

## Contribuir

Ver [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Licencia

[MIT](LICENSE) — ver el archivo `LICENSE` para el texto completo.
