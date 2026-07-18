package explodingkittens

import (
	"math/rand"
	"time"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

func newEvent(t string, payload any, recipients []engine.PlayerID) engine.Event {
	return engine.Event{Type: t, Timestamp: time.Now(), Payload: payload, Recipients: recipients}
}

// processAction : ActionProcessor.process en Dart (engine/action_processor.dart).
func processAction(a TurnAction, s State) (State, []engine.Event) {
	switch a.Type {
	case ActionDrawCard:
		return processDrawCard(a, s)
	case ActionPlayCard:
		return processPlayCard(a, s)
	case ActionPlayFavor:
		return processPlayFavor(a, s)
	case ActionPlayCatPair, ActionPlayCatTrio:
		return processCatGroup(a, s)
	case ActionDefuseBomb:
		return processDefuse(a, s)
	case ActionNope:
		return processNope(a, s)
	case ActionChooseCard:
		return processChooseCard(a, s)
	default:
		// Inalcanzable: decodeAction ya rechazó cualquier Type desconocido.
		return s, nil
	}
}

func processDrawCard(a TurnAction, s State) (State, []engine.Event) {
	deck, drawn, err := drawTop(s.Deck)
	if err != nil {
		// El mazo no debería vaciarse nunca en una partida bien formada
		// (checkWinCondition corta en cuanto queda un solo jugador vivo,
		// mucho antes de agotar las cartas); si pasa, no hay forma válida
		// de seguir — no tocamos el estado.
		return s, nil
	}
	next := s
	next.Deck = deck

	events := []engine.Event{newEvent(EventCardDrawn, CardDrawnPayload{PlayerID: a.PlayerID}, nil)}

	if drawn.Type == CardExplodingKitten {
		events = append(events, newEvent(EventBombTriggered, BombTriggeredPayload{PlayerID: a.PlayerID}, nil))

		player, _ := next.PlayerByID(a.PlayerID)
		if !player.HasCard(CardDefuse) {
			next = eliminatePlayer(a.PlayerID, next)
			events = append(events, newEvent(EventPlayerEliminated,
				PlayerEliminatedPayload{PlayerID: a.PlayerID, PlayerName: player.Name}, nil))

			if result := checkWinCondition(next); result != nil {
				events = append(events, newEvent(EventGameOver,
					GameOverPayload{WinnerID: result.WinnerID, WinnerName: result.WinnerName}, nil))
				next.Phase = PhaseFinished
				next.Result = result
				return next, events
			}
			return turnAdvance(next), events
		}

		// Tiene Defuse -> espera DefuseBombAction (el jugador elige la
		// posición). Se guarda la bomba robada para reinsertar exactamente
		// esa carta.
		next.Turn.Phase = TurnResolving
		bomb := drawn
		next.PendingBomb = &bomb
		return next, events
	}

	// Carta normal robada -> añadir a la mano y terminar el turno.
	next.Players = addCardToHand(a.PlayerID, drawn, next.Players)
	return turnAdvance(next), events
}

func processPlayCard(a TurnAction, s State) (State, []engine.Event) {
	card := cardOrZero(a.Card)
	next := removeCardFromHand(a.PlayerID, card.ID, s)
	next.Deck = discardCard(next.Deck, card)

	events := []engine.Event{newEvent(EventCardPlayed, CardPlayedPayload{PlayerID: a.PlayerID, Card: card}, nil)}

	switch card.Type {
	case CardAttack:
		n, ev := applyAttack(next)
		return n, append(events, ev...)
	case CardSkip:
		return turnAdvance(next), events
	case CardSeeTheFuture:
		n, ev := applySeeTheFuture(a.PlayerID, next)
		return n, append(events, ev...)
	default:
		// Shuffle y cualquier otra carta cuyo efecto se difiera: se resuelve
		// en resolveNopeWindow para que un Nope pueda cancelarlo.
		return turnOpenNopeWindow(next, a), events
	}
}

func processPlayFavor(a TurnAction, s State) (State, []engine.Event) {
	card := cardOrZero(a.Card)
	next := removeCardFromHand(a.PlayerID, card.ID, s)
	next.Deck = discardCard(next.Deck, card)

	events := []engine.Event{newEvent(EventCardPlayed, CardPlayedPayload{PlayerID: a.PlayerID, Card: card}, nil)}
	// El robo de la carta objetivo se difiere: si lo nopean, no se roba nada.
	return turnOpenNopeWindow(next, a), events
}

// processCatGroup: par y trío de gatos comparten la misma mecánica (robo
// diferido a la resolución de la ventana de Nope) y, al igual que en el
// original, no emiten CardPlayedEvent — solo PlayCardAction/PlayFavorAction
// lo hacen ahí.
func processCatGroup(a TurnAction, s State) (State, []engine.Event) {
	next := s
	for _, c := range a.Cards {
		next = removeCardFromHand(a.PlayerID, c.ID, next)
		next.Deck = discardCard(next.Deck, c)
	}
	return turnOpenNopeWindow(next, a), nil
}

func processDefuse(a TurnAction, s State) (State, []engine.Event) {
	defuseCard := cardOrZero(a.DefuseCard)
	next := removeCardFromHand(a.PlayerID, defuseCard.ID, s)

	// Reinsertar exactamente la bomba que se robó (PendingBomb), no otra
	// bomba cualquiera del mazo restante.
	if s.PendingBomb != nil {
		next.Deck = insertAt(next.Deck, *s.PendingBomb, a.InsertAtPosition)
		next.PendingBomb = nil
	}

	// A diferencia del original, este evento solo va a quien defusó — ver
	// el comentario en BombDefusedPayload.
	events := []engine.Event{newEvent(EventBombDefused,
		BombDefusedPayload{InsertedAtPosition: a.InsertAtPosition}, []engine.PlayerID{a.PlayerID})}
	return turnAdvance(next), events
}

func processNope(a TurnAction, s State) (State, []engine.Event) {
	nopeCard := cardOrZero(a.NopeCard)
	next := removeCardFromHand(a.PlayerID, nopeCard.ID, s)
	next.Deck = discardCard(next.Deck, nopeCard)

	newChain := incrementNopeChain(next.Turn.NopeChainCount)
	next.Turn.NopeChainCount = newChain

	events := []engine.Event{newEvent(EventNoped, NopedPayload{PlayerID: a.PlayerID, ChainCount: newChain}, nil)}
	return next, events
}

// resolveNopeWindowState : ActionProcessor.resolveNopeWindow en Dart. No es
// una acción de jugador (la dispara el timer del room cuando expira la
// ventana), así que no pasa por validateAction — igual que en el original.
//
// Si la cadena quedó cancelada (nopeChainCount impar) descarta el efecto
// pendiente sin aplicarlo; si no, lo aplica ahora — excepto Favor y trío de
// gatos, que en vez de robar al azar o una ya elegida dejan que alguien
// elija cuál (ver processChooseCard): la fase pasa a awaitingCardChoice sin
// limpiar PendingAction, porque todavía hace falta esa info para resolver.
func resolveNopeWindowState(s State) (State, []engine.Event) {
	next := s
	awaitingCardChoice := false
	var events []engine.Event

	if !s.Turn.IsNoped() {
		if pa, ok := s.PendingAction.(TurnAction); ok {
			switch pa.Type {
			case ActionPlayFavor:
				target, ok := next.PlayerByID(pa.Target())
				// Si el objetivo no tiene cartas, no hay nada que elegir —
				// se resuelve como no-op, igual que un par de gatos contra
				// una mano vacía.
				awaitingCardChoice = ok && len(target.Hand) > 0
			case ActionPlayCatPair:
				next = stealRandomCard(pa.PlayerID, pa.Target(), next)
			case ActionPlayCatTrio:
				// El actor elige a ciegas (no ve la mano rival); mismo caso
				// límite que Favor si el objetivo no tiene cartas.
				target, ok := next.PlayerByID(pa.Target())
				awaitingCardChoice = ok && len(target.Hand) > 0
			case ActionPlayCard:
				if pa.Card != nil && pa.Card.Type == CardShuffle {
					next, events = resolveShuffle(next)
				}
			}
		}
	}

	if awaitingCardChoice {
		next.Turn.Phase = TurnAwaitingCardChoice
		next.Turn.NopeChainCount = 0
		return next, events
	}

	next.Turn.Phase = TurnPlaying
	next.Turn.NopeChainCount = 0
	next.PendingAction = nil
	return next, events
}

// processChooseCard : elegir una carta concreta (Favor y trío de gatos).
func processChooseCard(a TurnAction, s State) (State, []engine.Event) {
	next := s
	if pa, ok := s.PendingAction.(TurnAction); ok {
		switch pa.Type {
		case ActionPlayFavor, ActionPlayCatTrio:
			next = stealChosenCard(pa.PlayerID, pa.Target(), a.CardID, s)
		}
	}
	next.Turn.Phase = TurnPlaying
	next.PendingAction = nil
	return next, nil
}

func stealRandomCard(playerID, targetID engine.PlayerID, s State) State {
	target, ok := s.PlayerByID(targetID)
	if !ok || len(target.Hand) == 0 {
		return s
	}
	stolen := target.Hand[rand.Intn(len(target.Hand))]
	next := removeCardFromHand(targetID, stolen.ID, s)
	next.Players = addCardToHand(playerID, stolen, next.Players)
	return next
}

func stealChosenCard(playerID, targetID engine.PlayerID, chosenCardID string, s State) State {
	target, ok := s.PlayerByID(targetID)
	if !ok {
		return s
	}
	var chosen *Card
	for _, c := range target.Hand {
		if c.ID == chosenCardID {
			cc := c
			chosen = &cc
			break
		}
	}
	if chosen == nil {
		return s
	}
	next := removeCardFromHand(targetID, chosen.ID, s)
	next.Players = addCardToHand(playerID, *chosen, next.Players)
	return next
}

func resolveShuffle(s State) (State, []engine.Event) {
	next := s
	next.Deck = shuffleDeck(s.Deck, rand.New(rand.NewSource(time.Now().UnixNano())))
	return next, []engine.Event{newEvent(EventDeckShuffled, struct{}{}, nil)}
}

func applyAttack(s State) (State, []engine.Event) {
	next := turnAdvance(s)
	// El siguiente jugador debe jugar 2 veces.
	next.Turn.ActionsLeft = 2
	return next, nil
}

func applySeeTheFuture(playerID engine.PlayerID, s State) (State, []engine.Event) {
	top3 := peekTop(s.Deck, 3)
	next := s
	next.SeeTheFutureCards = top3
	// Solo el jugador que jugó la carta puede ver el resultado — ver el
	// comentario en SeeTheFuturePayload.
	events := []engine.Event{newEvent(EventSeeTheFuture, SeeTheFuturePayload{TopCards: top3}, []engine.PlayerID{playerID})}
	return next, events
}

// Desconexión (grace period). Ninguna de las tres operaciones siguientes es
// una TurnAction: las dispara el timer de room (grace period expirado o
// reconexión a tiempo), no un jugador jugando su turno, así que tampoco
// pasan por validateAction — mismo patrón que resolveNopeWindowState.

func markDisconnected(playerID engine.PlayerID, s State) State {
	p, ok := s.PlayerByID(playerID)
	if !ok || p.Status != StatusActive {
		return s
	}
	return setStatus(playerID, StatusDisconnected, s)
}

func markReconnected(playerID engine.PlayerID, s State) State {
	p, ok := s.PlayerByID(playerID)
	if !ok || p.Status != StatusDisconnected {
		return s
	}
	return setStatus(playerID, StatusActive, s)
}

// eliminateForDisconnect reutiliza el mismo camino de eliminatePlayer que
// una bomba sin Defuse. Si el desconectado tenía el turno, lo pasa al
// siguiente jugador vivo; si no, el turno en curso no se ve afectado.
func eliminateForDisconnect(playerID engine.PlayerID, s State) (State, []engine.Event) {
	p, ok := s.PlayerByID(playerID)
	if !ok || p.Status == StatusEliminated {
		return s, nil
	}

	next := eliminatePlayer(playerID, s)
	events := []engine.Event{newEvent(EventPlayerEliminated,
		PlayerEliminatedPayload{PlayerID: playerID, PlayerName: p.Name}, nil)}

	if result := checkWinCondition(next); result != nil {
		events = append(events, newEvent(EventGameOver,
			GameOverPayload{WinnerID: result.WinnerID, WinnerName: result.WinnerName}, nil))
		next.Phase = PhaseFinished
		next.Result = result
		return next, events
	}

	if s.Turn.CurrentPlayerID == playerID {
		next = turnAdvance(next)
	}
	return next, events
}

// ── helpers de mutación (siempre devuelven un State nuevo) ─────────────────

func setStatus(playerID engine.PlayerID, status PlayerStatus, s State) State {
	next := s
	next.Players = mapPlayers(s.Players, func(p Player) Player {
		if p.ID == playerID {
			p.Status = status
		}
		return p
	})
	return next
}

func eliminatePlayer(playerID engine.PlayerID, s State) State {
	next := s
	next.Players = mapPlayers(s.Players, func(p Player) Player {
		if p.ID == playerID {
			p.Status = StatusEliminated
		}
		return p
	})
	next.EliminationOrder = append(append([]engine.PlayerID{}, s.EliminationOrder...), playerID)
	return next
}

func removeCardFromHand(playerID engine.PlayerID, cardID string, s State) State {
	next := s
	next.Players = mapPlayers(s.Players, func(p Player) Player {
		if p.ID != playerID {
			return p
		}
		hand := make([]Card, 0, len(p.Hand))
		for _, c := range p.Hand {
			if c.ID != cardID {
				hand = append(hand, c)
			}
		}
		p.Hand = hand
		return p
	})
	return next
}

func addCardToHand(playerID engine.PlayerID, card Card, players []Player) []Player {
	return mapPlayers(players, func(p Player) Player {
		if p.ID == playerID {
			p.Hand = append(append([]Card{}, p.Hand...), card)
		}
		return p
	})
}

func mapPlayers(players []Player, f func(Player) Player) []Player {
	out := make([]Player, len(players))
	for i, p := range players {
		out[i] = f(p)
	}
	return out
}

// turnAdvance : TurnManager.advance en Dart (turn/turn_manager.dart).
func turnAdvance(s State) State {
	next := s
	next.PendingAction = nil
	next.SeeTheFutureCards = nil
	next.TurnCount = s.TurnCount + 1

	if attacksLeft := attackTurnsLeft(s); attacksLeft > 0 {
		// El mismo jugador aún tiene turnos por el Attack.
		next.Turn = Turn{
			CurrentPlayerID: s.Turn.CurrentPlayerID,
			Phase:           TurnPlaying,
			ActionsLeft:     attacksLeft,
			NopeChainCount:  0,
		}
		return next
	}

	nextID, err := nextPlayerID(s)
	if err != nil {
		// No hay jugadores vivos: no debería pasar (checkWinCondition corta
		// en cuanto queda uno solo), pero si pasa no hay a quién pasarle el
		// turno.
		return next
	}
	next.Turn = Turn{CurrentPlayerID: nextID, Phase: TurnPlaying, ActionsLeft: 1, NopeChainCount: 0}
	return next
}

// turnOpenNopeWindow : TurnManager.openNopeWindow en Dart.
func turnOpenNopeWindow(s State, pendingAction TurnAction) State {
	next := s
	next.Turn.Phase = TurnNopeWindow
	next.PendingAction = pendingAction
	return next
}

// checkWinCondition : WinCondition.check en Dart (rules/win_condition.dart).
func checkWinCondition(s State) *Result {
	alive := s.AlivePlayers()
	if len(alive) != 1 {
		return nil
	}
	winner := alive[0]
	return &Result{
		WinnerID:         winner.ID,
		WinnerName:       winner.Name,
		TotalTurns:       s.TurnCount,
		EliminationOrder: append([]engine.PlayerID{}, s.EliminationOrder...),
	}
}
