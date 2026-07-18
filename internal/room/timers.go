package room

import (
	"time"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

// timerSet centraliza los timers de UNA sala: uno por jugador desconectado
// (grace period de reconexión) más, como mucho, uno para la ventana de
// reacción que pida el motor vía GameEngine.PendingTimer. Es un port
// generalizado de ReconnectionManager (Dart, network/reconnection/) — mismo
// patrón trackDisconnect/cancelIfPending, aplicado también al timer de
// reacción del motor.
//
// No necesita mutex: todos los métodos se llaman siempre desde el único
// goroutine dueño de la sala (ver Room.run), igual que el resto del estado.
type timerSet struct {
	reconnect map[engine.PlayerID]*time.Timer
	reaction  *time.Timer
}

func newTimerSet() *timerSet {
	return &timerSet{reconnect: make(map[engine.PlayerID]*time.Timer)}
}

// trackDisconnect arranca (o reinicia) el grace period de playerID; si no
// reconecta antes de que expire, llama a onExpired.
func (t *timerSet) trackDisconnect(playerID engine.PlayerID, d time.Duration, onExpired func()) {
	if existing, ok := t.reconnect[playerID]; ok {
		existing.Stop()
	}
	t.reconnect[playerID] = time.AfterFunc(d, onExpired)
}

// cancelDisconnect cancela el grace period de playerID si había uno
// corriendo (reconectó a tiempo). No hace nada si no había ninguno pendiente.
func (t *timerSet) cancelDisconnect(playerID engine.PlayerID) {
	if existing, ok := t.reconnect[playerID]; ok {
		existing.Stop()
		delete(t.reconnect, playerID)
	}
}

// setReaction reprograma el timer de la ventana de reacción del motor: cada
// llamada cancela el anterior antes de, si ok, arrancar uno nuevo — el
// motor decide en cada Apply si la ventana sigue abierta y cuánto le queda,
// el room solo obedece.
func (t *timerSet) setReaction(d time.Duration, ok bool, onExpired func()) {
	if t.reaction != nil {
		t.reaction.Stop()
		t.reaction = nil
	}
	if ok {
		t.reaction = time.AfterFunc(d, onExpired)
	}
}

func (t *timerSet) stopAll() {
	for _, tm := range t.reconnect {
		tm.Stop()
	}
	if t.reaction != nil {
		t.reaction.Stop()
	}
}
