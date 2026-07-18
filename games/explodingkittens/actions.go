package explodingkittens

import (
	"encoding/json"
	"fmt"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

// ActionType : discriminante "type" de TurnAction en Dart (turn_action.dart).
type ActionType string

const (
	ActionDrawCard    ActionType = "draw_card"
	ActionPlayCard    ActionType = "play_card"
	ActionPlayFavor   ActionType = "play_favor"
	ActionPlayCatPair ActionType = "play_cat_pair"
	ActionPlayCatTrio ActionType = "play_cat_trio"
	ActionDefuseBomb  ActionType = "defuse_bomb"
	ActionNope        ActionType = "nope"
	ActionChooseCard  ActionType = "choose_card"
)

// TurnAction es la unión de todas las acciones de jugador, aplanada en un
// único struct. Dart modela esto como una jerarquía sellada (una clase por
// variante); Go no tiene sum types, así que el equivalente idiomático es un
// struct con el discriminante Type y los campos relevantes según el caso
// (nil/"" en los que no aplican) — mismo contrato de red, distinta
// representación interna.
type TurnAction struct {
	Type ActionType `json:"type"`

	// El actor lo determina la conexión autenticada (room), nunca el
	// payload entrante — así un jugador no puede actuar en nombre de otro.
	PlayerID engine.PlayerID `json:"-"`

	Card             *Card  `json:"card,omitempty"`             // play_card
	Cards            []Card `json:"cards,omitempty"`            // play_cat_pair / play_cat_trio
	TargetPlayerID   string `json:"targetPlayerId,omitempty"`   // play_favor / play_cat_pair / play_cat_trio
	DefuseCard       *Card  `json:"defuseCard,omitempty"`       // defuse_bomb
	InsertAtPosition int    `json:"insertAtPosition,omitempty"` // defuse_bomb
	NopeCard         *Card  `json:"nopeCard,omitempty"`         // nope
	CardID           string `json:"cardId,omitempty"`           // choose_card
}

func (a TurnAction) Target() engine.PlayerID { return engine.PlayerID(a.TargetPlayerID) }

// decodeAction : equivalente a TurnAction.fromJson, pero el actor se
// sobrescribe siempre con el playerId de la conexión.
func decodeAction(actor engine.PlayerID, raw json.RawMessage) (TurnAction, error) {
	var a TurnAction
	if err := json.Unmarshal(raw, &a); err != nil {
		return TurnAction{}, fmt.Errorf("explodingkittens: acción inválida: %w", err)
	}
	switch a.Type {
	case ActionDrawCard, ActionPlayCard, ActionPlayFavor, ActionPlayCatPair,
		ActionPlayCatTrio, ActionDefuseBomb, ActionNope, ActionChooseCard:
	default:
		return TurnAction{}, fmt.Errorf("explodingkittens: tipo de acción desconocido: %q", a.Type)
	}
	a.PlayerID = actor
	return a, nil
}
