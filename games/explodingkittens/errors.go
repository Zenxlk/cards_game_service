package explodingkittens

import "fmt"

// InvalidActionError: la acción fue rechazada por las reglas (jugada
// ilegal, no le toca al jugador, etc.). El room la traduce en un mensaje de
// rechazo al jugador que la mandó, sin tocar el estado — GameEngine.Apply
// nunca cambia state si devuelve este error.
type InvalidActionError struct{ Message string }

func (e *InvalidActionError) Error() string { return e.Message }

func invalidAction(format string, args ...any) error {
	return &InvalidActionError{Message: fmt.Sprintf(format, args...)}
}

// GameError: invariante interna rota (p. ej. un playerId que no existe en
// la partida). A diferencia de InvalidActionError, esto no debería poder
// pasar nunca si room valida los ids antes de invocar al motor — si
// aparece, es un bug del server, no una jugada inválida del cliente.
type GameError struct{ Message string }

func (e *GameError) Error() string { return e.Message }

func gameError(format string, args ...any) error {
	return &GameError{Message: fmt.Sprintf(format, args...)}
}
