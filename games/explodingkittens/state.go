package explodingkittens

import "github.com/ZenXLK/cards-game-service/pkg/engine"

// GamePhase : GamePhase en Dart (game_state.dart).
type GamePhase string

const (
	PhasePlaying  GamePhase = "playing"
	PhaseFinished GamePhase = "finished"
)

// TurnPhase : TurnPhase en Dart (turn_model.dart). Se porta el enum completo
// tal cual, incluido drawRequired aunque hoy ninguna transición lo produzca
// en el original — mantenerlo evita que el wire format diverja si el motor
// Dart llega a usarlo más adelante.
type TurnPhase string

const (
	TurnPlaying            TurnPhase = "playing"
	TurnResolving          TurnPhase = "resolving"
	TurnNopeWindow         TurnPhase = "nope_window"
	TurnAwaitingCardChoice TurnPhase = "awaiting_card_choice"
	TurnDrawRequired       TurnPhase = "draw_required"
	TurnEnded              TurnPhase = "ended"
)

// PlayerStatus : PlayerStatus en Dart.
type PlayerStatus string

const (
	StatusActive       PlayerStatus = "active"
	StatusDisconnected PlayerStatus = "disconnected"
	StatusEliminated   PlayerStatus = "eliminated"
)

// Player : PlayerModel en Dart.
type Player struct {
	ID     engine.PlayerID `json:"id"`
	Name   string          `json:"name"`
	Hand   []Card          `json:"hand"`
	Status PlayerStatus    `json:"status"`
}

func (p Player) IsAlive() bool { return p.Status == StatusActive }

func (p Player) HasCard(cardType CardType) bool {
	for _, c := range p.Hand {
		if c.Type == cardType {
			return true
		}
	}
	return false
}

func (p Player) HasCardID(id string) bool {
	for _, c := range p.Hand {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Deck : DeckModel en Dart.
type Deck struct {
	DrawPile    []Card `json:"drawPile"`
	DiscardPile []Card `json:"discardPile"`
}

// Turn : TurnModel en Dart.
type Turn struct {
	CurrentPlayerID engine.PlayerID `json:"currentPlayerId"`
	Phase           TurnPhase       `json:"phase"`
	ActionsLeft     int             `json:"actionsLeft"`    // veces que debe robar (cadenas de Attack)
	NopeChainCount  int             `json:"nopeChainCount"` // número de Nopes en cadena (par = cancelado)
}

func (t Turn) IsNoped() bool { return t.NopeChainCount%2 == 1 }

// Config : GameConfig en Dart. Seed es opcional — con ella, DeckBuilder.Build
// es determinista (clave para tests sin mocks, ver deck_test.go).
type Config struct {
	Seed *int64 `json:"seed,omitempty"`
}

// Result : GameResult en Dart.
type Result struct {
	WinnerID         engine.PlayerID   `json:"winnerId"`
	WinnerName       string            `json:"winnerName"`
	TotalTurns       int               `json:"totalTurns"`
	EliminationOrder []engine.PlayerID `json:"eliminationOrder"`
}

// State : GameState en Dart (game_state.dart). Implementa engine.Terminal.
type State struct {
	ID      string    `json:"id"`
	Config  Config    `json:"config"`
	Players []Player  `json:"players"`
	Deck    Deck      `json:"deck"`
	Turn    Turn      `json:"turn"`
	Phase   GamePhase `json:"phase"`

	// PendingAction: acción en espera de resolución (ventana de Nope
	// abierta, o pendiente de que alguien elija una carta). Siempre un
	// *TurnAction en la práctica — any porque engine.State es opaco fuera
	// de este paquete, igual que Object? en el GameState de Dart.
	PendingAction any `json:"pendingAction,omitempty"`

	// PendingBomb: el Exploding Kitten exacto que se robó, en espera de
	// reinserción (Defuse en curso). Se reinserta esta carta puntual, no
	// una bomba cualquiera del mazo restante.
	PendingBomb *Card `json:"pendingBomb,omitempty"`

	// SeeTheFutureCards: top 3 cartas visibles tras jugar Ver el Futuro.
	// Solo debe llegar al jugador que la jugó — ver View() en engine.go.
	SeeTheFutureCards []Card `json:"seeTheFutureCards,omitempty"`

	Result    *Result `json:"result,omitempty"`
	TurnCount int     `json:"turnCount"`

	// EliminationOrder: orden cronológico real de eliminación (a diferencia
	// de recorrer Players filtrando por status).
	EliminationOrder []engine.PlayerID `json:"eliminationOrder"`
}

func (s State) Terminal() bool { return s.Phase == PhaseFinished }

func (s State) PlayerByID(id engine.PlayerID) (Player, bool) {
	for _, p := range s.Players {
		if p.ID == id {
			return p, true
		}
	}
	return Player{}, false
}

func (s State) CurrentPlayer() (Player, bool) {
	return s.PlayerByID(s.Turn.CurrentPlayerID)
}

func (s State) AlivePlayers() []Player {
	alive := make([]Player, 0, len(s.Players))
	for _, p := range s.Players {
		if p.Status == StatusActive {
			alive = append(alive, p)
		}
	}
	return alive
}

// withPlayers, withDeck, etc. no existen como copyWith genérico a propósito:
// en Go es más simple que cada transición en process.go construya el State
// siguiente por struct literal, copiando explícitamente los campos que
// cambian. Evita el estado intermedio "cuál puntero es nil vs cuál no tocar"
// que copyWith resuelve en Dart con sus flags clearX.
