package explodingkittens

import "github.com/ZenXLK/cards_game_service/pkg/engine"

// Tipos de evento : discriminante "type" de GameEvent en Dart (game_event.dart).
const (
	EventCardPlayed       = "card_played"
	EventCardDrawn        = "card_drawn"
	EventBombTriggered    = "bomb_triggered"
	EventBombDefused      = "bomb_defused"
	EventPlayerEliminated = "player_eliminated"
	EventNoped            = "noped"
	EventTurnChanged      = "turn_changed"
	EventGameOver         = "game_over"
	EventDeckShuffled     = "deck_shuffled"
	EventSeeTheFuture     = "see_the_future"
)

type CardPlayedPayload struct {
	PlayerID engine.PlayerID `json:"playerId"`
	Card     Card            `json:"card"`
}

type CardDrawnPayload struct {
	PlayerID engine.PlayerID `json:"playerId"`
}

type BombTriggeredPayload struct {
	PlayerID engine.PlayerID `json:"playerId"`
}

// BombDefusedPayload: a diferencia del GameEvent original en Dart (que
// incluye InsertedAtPosition en un evento broadcast a todos), este payload
// SOLO va a quien defusó — ver Recipients al emitirlo en process.go. En las
// reglas reales la reinserción es secreta; el original la filtraba porque en
// LAN confía en el cliente para no mostrarla, algo que no vale para internet
// abierto.
type BombDefusedPayload struct {
	InsertedAtPosition int `json:"insertedAtPosition"`
}

type PlayerEliminatedPayload struct {
	PlayerID   engine.PlayerID `json:"playerId"`
	PlayerName string          `json:"playerName"`
}

type NopedPayload struct {
	PlayerID   engine.PlayerID `json:"playerId"`
	ChainCount int             `json:"chainCount"`
}

type TurnChangedPayload struct {
	NextPlayerID engine.PlayerID `json:"nextPlayerId"`
	TurnCount    int             `json:"turnCount"`
}

type GameOverPayload struct {
	WinnerID   engine.PlayerID `json:"winnerId"`
	WinnerName string          `json:"winnerName"`
}

// SeeTheFuturePayload: a diferencia del original (que viaja dentro del
// GameState completo y se broadcastea a todos), acá solo debe llegar al
// jugador que jugó la carta — mismo motivo que BombDefusedPayload.
type SeeTheFuturePayload struct {
	TopCards []Card `json:"topCards"`
}
