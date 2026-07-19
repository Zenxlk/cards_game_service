// Package fixture es un engine.GameEngine mínimo, sin información oculta y
// sin más regla que "sumá puntos y pasá el turno". Existe solo para que los
// tests de internal/room e internal/transport (test/integration/) puedan
// probar transporte, ciclo de vida de sala y reconexión sin arrastrar la
// complejidad de las reglas reales de explodingkittens — un bug en Exploding
// Kittens no debería poder romper un test que en realidad prueba WebSocket.
package fixture

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

const GameTypeName = "fixture"

func init() {
	engine.Register(GameTypeName, NewEngine)
}

type Engine struct{}

func NewEngine(_ json.RawMessage) (engine.GameEngine, error) { return Engine{}, nil }

type PlayerState struct {
	ID     engine.PlayerID `json:"id"`
	Name   string          `json:"name"`
	Score  int             `json:"score"`
	Active bool            `json:"active"`
}

type State struct {
	Players         []PlayerState   `json:"players"`
	CurrentPlayerID engine.PlayerID `json:"currentPlayerId"`
	Round           int             `json:"round"`
	MaxRounds       int             `json:"maxRounds"`
	Done            bool            `json:"done"`
}

func (s State) Terminal() bool { return s.Done }

type Action struct {
	PlayerID engine.PlayerID `json:"-"`
	Points   int             `json:"points"`
}

func (Engine) Start(players []engine.PlayerInfo, _ json.RawMessage) (engine.State, error) {
	if len(players) < 2 {
		return nil, fmt.Errorf("fixture: se requieren al menos 2 jugadores")
	}
	ps := make([]PlayerState, len(players))
	for i, p := range players {
		ps[i] = PlayerState{ID: p.ID, Name: p.Name, Active: true}
	}
	return State{Players: ps, CurrentPlayerID: ps[0].ID, MaxRounds: 3}, nil
}

func (Engine) DecodeAction(actor engine.PlayerID, raw json.RawMessage) (engine.Action, error) {
	var a Action
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("fixture: acción inválida: %w", err)
		}
	}
	a.PlayerID = actor
	return a, nil
}

func (Engine) Apply(state engine.State, action engine.Action) (engine.State, []engine.Event, error) {
	s, ok := state.(State)
	if !ok {
		return state, nil, fmt.Errorf("fixture: estado inesperado %T", state)
	}
	a, ok := action.(Action)
	if !ok {
		return state, nil, fmt.Errorf("fixture: acción inesperada %T", action)
	}
	if a.PlayerID != s.CurrentPlayerID {
		return state, nil, fmt.Errorf("fixture: no es el turno de %s", a.PlayerID)
	}

	next := s
	next.Players = append([]PlayerState{}, s.Players...)

	nextIdx := 0
	for i, p := range next.Players {
		if p.ID == a.PlayerID {
			next.Players[i].Score += a.Points
			nextIdx = (i + 1) % len(next.Players)
		}
	}
	next.CurrentPlayerID = next.Players[nextIdx].ID
	if nextIdx == 0 {
		next.Round++
	}
	next.Done = next.Round >= next.MaxRounds

	events := []engine.Event{{Type: "scored", Timestamp: time.Now(), Payload: map[string]any{"playerId": a.PlayerID, "points": a.Points}}}
	return next, events, nil
}

// ResolveNopeWindow: fixture no tiene ventanas de reacción, es un no-op.
func (Engine) ResolveNopeWindow(state engine.State) (engine.State, []engine.Event, error) {
	return state, nil, nil
}

func (Engine) MarkPlayerDisconnected(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	return setActive(state, player, false)
}

func (Engine) MarkPlayerReconnected(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	return setActive(state, player, true)
}

func (Engine) EliminateForDisconnect(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	next, _, err := setActive(state, player, false)
	if err != nil {
		return state, nil, err
	}
	return next, []engine.Event{{Type: "eliminated", Timestamp: time.Now(), Payload: map[string]any{"playerId": player}}}, nil
}

// View: sin información oculta, la vista es el estado completo.
func (Engine) View(state engine.State, _ engine.PlayerID) (any, error) {
	return state, nil
}

// PendingTimer: fixture no tiene ventanas de reacción con límite de tiempo.
func (Engine) PendingTimer(engine.State) (time.Duration, bool) { return 0, false }

func setActive(state engine.State, player engine.PlayerID, active bool) (engine.State, []engine.Event, error) {
	s, ok := state.(State)
	if !ok {
		return state, nil, fmt.Errorf("fixture: estado inesperado %T", state)
	}
	next := s
	next.Players = append([]PlayerState{}, s.Players...)
	for i, p := range next.Players {
		if p.ID == player {
			next.Players[i].Active = active
		}
	}
	return next, nil, nil
}
