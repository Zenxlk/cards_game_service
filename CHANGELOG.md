# Changelog

Formato basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/),
versionado según [Semantic Versioning](https://semver.org/lang/es/).

## [Unreleased]

Motor de reglas de Exploding Kittens: casos límite adicionales, más tests de
integración de reconexión.

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

[Unreleased]: https://github.com/ZenXLK/cards_game_service/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/ZenXLK/cards_game_service/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ZenXLK/cards_game_service/releases/tag/v0.1.0
