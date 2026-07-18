// Package engine define el contrato que cualquier juego de cartas concreto
// implementa para conectarse al servidor. El servidor (room/lobby/transport)
// solo conoce este paquete — nunca las reglas de un juego concreto.
package engine

import (
	"encoding/json"
	"time"
)

// PlayerID identifica a un participante dentro de una partida. Lo asigna
// room/lobby antes de llamar a Start; el motor lo trata como opaco.
type PlayerID string

// PlayerInfo es la información mínima de un jugador que cualquier motor
// necesita para arrancar una partida: identidad y nombre a mostrar. No es
// específico de ningún juego — prácticamente todo juego de cartas necesita
// mostrar nombres en sus eventos (p. ej. "X fue eliminado").
type PlayerInfo struct {
	ID   PlayerID `json:"id"`
	Name string   `json:"name"`
}

// State es el estado completo de una partida. Cada motor define su propio
// tipo concreto; debe ser serializable a JSON. El motor nunca lo guarda
// como campo propio: siempre se pasa y se devuelve explícitamente, así
// Apply queda determinista y testeable sin goroutines ni I/O.
type State any

// Action es la entrada decodificada de un jugador. Cada motor define su
// propio tipo concreto (normalmente una unión discriminada por "type").
type Action any

// Event es un efecto secundario producido por Apply u otra transición,
// distinto del estado en sí (p. ej. "se jugó una carta", "bomba detonada").
type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// Recipients restringe a quién se le envía este evento. Vacío/nil
	// significa "a todos los jugadores de la partida". Todo evento que
	// exponga información oculta (p. ej. las cartas vistas con "ver el
	// futuro") DEBE declarar sus destinatarios explícitamente — el broadcast
	// nunca decide esto por su cuenta.
	Recipients []PlayerID `json:"-"`

	Payload any `json:"payload"`
}

// Terminal es una interfaz opcional que un State concreto puede implementar
// para señalar que la partida terminó. Si el State no la implementa, el
// room nunca da la partida por terminada automáticamente.
type Terminal interface {
	Terminal() bool
}

// GameEngine es el contrato de reglas puro que implementa cada juego
// concreto. Ninguno de sus métodos hace I/O ni lanza goroutines: misma
// entrada, misma salida, siempre — así son deterministas y testeables sin
// levantar red ni goroutines.
type GameEngine interface {
	// Start crea el estado inicial para los jugadores dados. config es la
	// configuración específica del juego (p. ej. semilla de barajado),
	// decodificada por el propio motor.
	Start(players []PlayerInfo, config json.RawMessage) (State, error)

	// DecodeAction interpreta el payload crudo recibido por WebSocket. El
	// transporte no conoce la forma de las acciones de este juego; solo el
	// motor sabe decodificarlas.
	DecodeAction(actor PlayerID, raw json.RawMessage) (Action, error)

	// Apply valida y aplica una acción de jugador contra el estado actual.
	// Devuelve un error si la acción no es válida (se traduce en un mensaje
	// de rechazo al jugador, sin tocar el estado).
	Apply(state State, action Action) (State, []Event, error)

	// ResolveNopeWindow, MarkPlayerDisconnected, MarkPlayerReconnected y
	// EliminateForDisconnect son transiciones que NO son acciones de
	// jugador: las dispara un timer del room (ventana de reacción agotada,
	// grace period de reconexión), no un jugador jugando su turno. Por eso
	// están separadas de Apply y no pasan por su misma validación.
	ResolveNopeWindow(state State) (State, []Event, error)
	MarkPlayerDisconnected(state State, player PlayerID) (State, []Event, error)
	MarkPlayerReconnected(state State, player PlayerID) (State, []Event, error)
	EliminateForDisconnect(state State, player PlayerID) (State, []Event, error)

	// View proyecta el estado completo a lo que un jugador concreto puede
	// ver — oculta manos ajenas, orden del mazo, etc. Es lo único que se
	// transmite por WebSocket; el room nunca reenvía State crudo.
	View(state State, viewer PlayerID) (any, error)

	// PendingTimer le dice al room si el estado actual necesita un timer de
	// seguimiento (p. ej. una ventana de reacción con límite de tiempo) y
	// cuánto debe durar. El room no interpreta el estado del juego — nunca
	// sabe qué es una "ventana de Nope" — solo programa un timer genérico
	// que, al expirar, llama a ResolveNopeWindow. Cada Apply reemplaza
	// cualquier timer previo: ok=false lo cancela sin reemplazo.
	PendingTimer(state State) (d time.Duration, ok bool)
}
