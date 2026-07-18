package explodingkittens

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

// GameTypeName es el identificador con el que este motor se registra en
// engine.Register — el mismo valor que el lobby espera al crear una sala.
const GameTypeName = "exploding_kittens"

func init() {
	engine.Register(GameTypeName, NewEngine)
}

// Engine implementa engine.GameEngine. No guarda estado propio — todos los
// métodos son funciones puras sobre el State recibido; equivalente al
// GameEngine de Dart, pero sin el bus de eventos global de por vida del
// proceso (ver el comentario de riesgo de lifecycle en el original).
type Engine struct{}

func NewEngine(_ json.RawMessage) (engine.GameEngine, error) {
	return Engine{}, nil
}

func (Engine) Start(players []engine.PlayerInfo, rawConfig json.RawMessage) (engine.State, error) {
	if len(players) < MinPlayers || len(players) > MaxPlayers {
		return nil, invalidAction("se requieren %d-%d jugadores, llegaron %d", MinPlayers, MaxPlayers, len(players))
	}

	var cfg Config
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return nil, fmt.Errorf("explodingkittens: config inválida: %w", err)
		}
	}

	roster := make([]Player, len(players))
	for i, p := range players {
		roster[i] = Player{ID: p.ID, Name: p.Name, Status: StatusActive}
	}

	deck, dealt := buildDeck(roster, cfg)

	return State{
		ID:      newGameID(),
		Config:  cfg,
		Players: dealt,
		Deck:    deck,
		Turn: Turn{
			CurrentPlayerID: dealt[0].ID,
			Phase:           TurnPlaying,
			ActionsLeft:     1,
		},
		Phase:            PhasePlaying,
		EliminationOrder: []engine.PlayerID{},
	}, nil
}

func (Engine) DecodeAction(actor engine.PlayerID, raw json.RawMessage) (engine.Action, error) {
	return decodeAction(actor, raw)
}

func (Engine) Apply(state engine.State, action engine.Action) (engine.State, []engine.Event, error) {
	s, a, err := castStateAction(state, action)
	if err != nil {
		return state, nil, err
	}
	if err := validateAction(a, s); err != nil {
		return state, nil, err
	}
	next, events := processAction(a, s)
	return next, events, nil
}

func (Engine) ResolveNopeWindow(state engine.State) (engine.State, []engine.Event, error) {
	s, err := castState(state)
	if err != nil {
		return state, nil, err
	}
	next, events := resolveNopeWindowState(s)
	return next, events, nil
}

func (Engine) MarkPlayerDisconnected(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	s, err := castState(state)
	if err != nil {
		return state, nil, err
	}
	return markDisconnected(player, s), nil, nil
}

func (Engine) MarkPlayerReconnected(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	s, err := castState(state)
	if err != nil {
		return state, nil, err
	}
	return markReconnected(player, s), nil, nil
}

func (Engine) EliminateForDisconnect(state engine.State, player engine.PlayerID) (engine.State, []engine.Event, error) {
	s, err := castState(state)
	if err != nil {
		return state, nil, err
	}
	next, events := eliminateForDisconnect(player, s)
	return next, events, nil
}

func (Engine) View(state engine.State, viewer engine.PlayerID) (any, error) {
	s, err := castState(state)
	if err != nil {
		return nil, err
	}
	return newView(s, viewer), nil
}

// PendingTimer: la única transición con límite de tiempo en Exploding
// Kittens es la ventana de Nope. Duration calca GameConstants.nopeWindowMs
// del cliente Dart — el room la reprograma en cada acción, igual que el
// Timer de GameNotifier en el original (ver el comentario en
// GameRules._mustBeInPhase).
func (Engine) PendingTimer(state engine.State) (time.Duration, bool) {
	s, err := castState(state)
	if err != nil {
		return 0, false
	}
	if s.Turn.Phase == TurnNopeWindow {
		return NopeWindowMS * time.Millisecond, true
	}
	return 0, false
}

func castState(state engine.State) (State, error) {
	s, ok := state.(State)
	if !ok {
		return State{}, gameError("estado inesperado para explodingkittens.Engine: %T", state)
	}
	return s, nil
}

func castStateAction(state engine.State, action engine.Action) (State, TurnAction, error) {
	s, err := castState(state)
	if err != nil {
		return State{}, TurnAction{}, err
	}
	a, ok := action.(TurnAction)
	if !ok {
		return State{}, TurnAction{}, gameError("acción inesperada para explodingkittens.Engine: %T", action)
	}
	return s, a, nil
}
