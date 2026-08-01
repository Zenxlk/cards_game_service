package explodingkittens

import "github.com/ZenXLK/cards_game_service/pkg/engine"

// View es la proyección de State que efectivamente viaja por WebSocket — a
// diferencia del GameState completo que el WsServer original broadcastea
// tal cual (ver el hallazgo de diseño: eso funciona en LAN entre amigos
// porque confía en el cliente, pero no alcanza para internet abierto).
//
// Nunca incluye Config: Config.Seed permitiría a un cliente que conozca el
// algoritmo de barajado reconstruir el mazo entero, anulando el resto del
// ocultamiento.
type View struct {
	ID      string       `json:"id"`
	Players []PlayerView `json:"players"`

	DeckSize   int   `json:"deckSize"`
	DiscardTop *Card `json:"discardTop,omitempty"`

	Turn  Turn      `json:"turn"`
	Phase GamePhase `json:"phase"`

	// PendingAction: quién jugó qué tipo de carta contra quién es información
	// pública (se ve en la mesa); las cartas concretas involucradas, no.
	PendingAction *PendingActionView `json:"pendingAction,omitempty"`

	// PendingBomb: que hay una bomba pendiente de esconder ya es público
	// (bomb_triggered se emite a todos); no hace falta mandar la carta.
	PendingBomb bool `json:"pendingBomb"`

	// SeeTheFutureCards solo viaja para quien jugó la carta.
	SeeTheFutureCards []Card `json:"seeTheFutureCards,omitempty"`

	Result           *Result           `json:"result,omitempty"`
	TurnCount        int               `json:"turnCount"`
	EliminationOrder []engine.PlayerID `json:"eliminationOrder"`
}

type PlayerView struct {
	ID       engine.PlayerID `json:"id"`
	Name     string          `json:"name"`
	HandSize int             `json:"handSize"`
	// Hand solo se completa para el propio viewer; para el resto queda nil
	// y solo se conoce HandSize.
	Hand []Card `json:"hand,omitempty"`
	// HiddenHandIds: excepción puntual a la regla de arriba — el id (sin
	// type) de cada carta de esta mano, sin revelar cuál es cuál. Solo se
	// completa cuando el viewer tiene un trío de gatos pendiente contra
	// este jugador (ver blindCatTrioTarget) — es la única acción que
	// necesita un cardId real de una mano ajena para resolverse
	// (stealChosenCard busca por ID exacto en process.go); sin esto, el
	// cliente online no tenía forma de armar un ChooseCardAction válido.
	HiddenHandIds []string     `json:"hiddenHandIds,omitempty"`
	Status        PlayerStatus `json:"status"`
}

type PendingActionView struct {
	Type           ActionType      `json:"type"`
	PlayerID       engine.PlayerID `json:"playerId"`
	TargetPlayerID engine.PlayerID `json:"targetPlayerId,omitempty"`
}

func newView(s State, viewer engine.PlayerID) View {
	blindPickTarget := blindCatTrioTarget(s, viewer)

	players := make([]PlayerView, len(s.Players))
	for i, p := range s.Players {
		pv := PlayerView{ID: p.ID, Name: p.Name, HandSize: len(p.Hand), Status: p.Status}
		switch {
		case p.ID == viewer:
			pv.Hand = append([]Card{}, p.Hand...)
		case blindPickTarget != "" && p.ID == blindPickTarget:
			pv.HiddenHandIds = handIDs(p.Hand)
		}
		players[i] = pv
	}

	var discardTop *Card
	if n := len(s.Deck.DiscardPile); n > 0 {
		c := s.Deck.DiscardPile[n-1]
		discardTop = &c
	}

	view := View{
		ID:               s.ID,
		Players:          players,
		DeckSize:         len(s.Deck.DrawPile),
		DiscardTop:       discardTop,
		Turn:             s.Turn,
		Phase:            s.Phase,
		PendingBomb:      s.PendingBomb != nil,
		Result:           s.Result,
		TurnCount:        s.TurnCount,
		EliminationOrder: append([]engine.PlayerID{}, s.EliminationOrder...),
	}

	if pa, ok := s.PendingAction.(TurnAction); ok {
		view.PendingAction = &PendingActionView{Type: pa.Type, PlayerID: pa.PlayerID, TargetPlayerID: pa.Target()}
	}

	// Quien jugó "Ver el futuro" siempre es el jugador con el turno en ese
	// momento (solo el jugador actual puede jugar cartas — ver canPlay), y
	// el campo se limpia en cuanto el turno avanza. Por eso alcanza con
	// comparar contra CurrentPlayerID sin guardar el autor por separado.
	if len(s.SeeTheFutureCards) > 0 && viewer == s.Turn.CurrentPlayerID {
		view.SeeTheFutureCards = append([]Card{}, s.SeeTheFutureCards...)
	}

	return view
}

// blindCatTrioTarget devuelve el ID del jugador cuya mano "viewer" puede
// elegir a ciegas ahora mismo (trío de gatos pendiente, viewer es quien
// elige) — "" si no aplica ninguno. Ver el comentario de HiddenHandIds.
func blindCatTrioTarget(s State, viewer engine.PlayerID) engine.PlayerID {
	if s.Turn.Phase != TurnAwaitingCardChoice {
		return ""
	}
	pa, ok := s.PendingAction.(TurnAction)
	if !ok || pa.Type != ActionPlayCatTrio || pa.PlayerID != viewer {
		return ""
	}
	return pa.Target()
}

func handIDs(hand []Card) []string {
	ids := make([]string, len(hand))
	for i, c := range hand {
		ids[i] = c.ID
	}
	return ids
}
