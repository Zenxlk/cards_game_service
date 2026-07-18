package explodingkittens

import (
	"errors"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

// validateAction : GameRules.validate en Dart (rules/game_rules.dart).
func validateAction(a TurnAction, s State) error {
	switch a.Type {
	case ActionDrawCard:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		return mustBeInPhase(s, TurnPlaying)

	case ActionPlayCard:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		return mustBeAbleToPlay(cardOrZero(a.Card), s)

	case ActionPlayFavor:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		if err := mustBeAbleToPlay(cardOrZero(a.Card), s); err != nil {
			return err
		}
		if err := targetMustBeAlive(a.Target(), s); err != nil {
			return err
		}
		return targetMustNotBeSelf(a.PlayerID, a.Target())

	case ActionPlayCatPair:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		if err := mustBeInPhase(s, TurnPlaying); err != nil {
			return err
		}
		if !isValidCatPair(a.Cards) {
			return invalidAction("Par de gatos inválido")
		}
		if err := playerMustHaveCards(a.PlayerID, a.Cards, s); err != nil {
			return err
		}
		if err := targetMustBeAlive(a.Target(), s); err != nil {
			return err
		}
		return targetMustNotBeSelf(a.PlayerID, a.Target())

	case ActionPlayCatTrio:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		if err := mustBeInPhase(s, TurnPlaying); err != nil {
			return err
		}
		if !isValidCatTrio(a.Cards) {
			return invalidAction("Trío de gatos inválido")
		}
		if err := playerMustHaveCards(a.PlayerID, a.Cards, s); err != nil {
			return err
		}
		if err := targetMustBeAlive(a.Target(), s); err != nil {
			return err
		}
		return targetMustNotBeSelf(a.PlayerID, a.Target())

	case ActionDefuseBomb:
		if err := mustBeCurrentPlayer(a.PlayerID, s); err != nil {
			return err
		}
		if err := mustBeInPhase(s, TurnResolving); err != nil {
			return err
		}
		if s.PendingBomb == nil {
			return invalidAction("No hay ninguna bomba pendiente de esconder")
		}
		cp, _ := s.CurrentPlayer()
		if !canDefuse(cp) {
			return invalidAction("No tienes Defuse")
		}

	case ActionNope:
		p, ok := s.PlayerByID(a.PlayerID)
		if !ok {
			return gameError("Jugador no encontrado")
		}
		if !canNope(p, s) {
			return invalidAction("No puedes jugar Nope ahora")
		}

	case ActionChooseCard:
		if err := mustBeInPhase(s, TurnAwaitingCardChoice); err != nil {
			return err
		}
		pa, ok := s.PendingAction.(TurnAction)
		if !ok {
			return invalidAction("No hay ninguna elección de carta pendiente")
		}
		switch pa.Type {
		case ActionPlayFavor:
			// Favor: elige el objetivo, desde su propia mano.
			if a.PlayerID != pa.Target() {
				return invalidAction("No te toca elegir una carta")
			}
			return playerMustHaveCard(pa.Target(), a.CardID, s)
		case ActionPlayCatTrio:
			// Trío de gatos: elige el actor (a ciegas), desde la mano rival.
			if a.PlayerID != pa.PlayerID {
				return invalidAction("No te toca elegir una carta")
			}
			return playerMustHaveCard(pa.Target(), a.CardID, s)
		default:
			return invalidAction("No hay ninguna elección de carta pendiente")
		}

	default:
		return invalidAction("Tipo de acción desconocido: %s", a.Type)
	}
	return nil
}

func mustBeCurrentPlayer(id engine.PlayerID, s State) error {
	if s.Turn.CurrentPlayerID != id {
		return invalidAction("No es tu turno")
	}
	return nil
}

// mustBeInPhase resguarda contra una acción "atrasada" por latencia de red
// que llega después de que la fase ya cambió (p. ej. un draw que llega justo
// cuando se acaba de abrir una ventana de Nope). Sin este candado explícito
// por tipo de acción, avanzar el turno podía limpiar PendingAction de golpe
// y dejar un Favor/par de gatos pendiente sin resolver — bug real que ya
// mordió al original, ver GameRules._mustBeInPhase.
func mustBeInPhase(s State, phase TurnPhase) error {
	if s.Turn.Phase != phase {
		return invalidAction("No puedes hacer eso ahora")
	}
	return nil
}

func mustBeAbleToPlay(c Card, s State) error {
	if !canPlay(c, s) {
		return invalidAction("No puedes jugar %s", c.Type)
	}
	return nil
}

func targetMustBeAlive(targetID engine.PlayerID, s State) error {
	target, ok := s.PlayerByID(targetID)
	if !ok || !target.IsAlive() {
		return invalidAction("El objetivo no está en juego")
	}
	return nil
}

func targetMustNotBeSelf(playerID, targetID engine.PlayerID) error {
	if playerID == targetID {
		return invalidAction("No puedes elegirte a ti mismo")
	}
	return nil
}

func playerMustHaveCards(playerID engine.PlayerID, cards []Card, s State) error {
	p, ok := s.PlayerByID(playerID)
	if !ok {
		return gameError("Jugador no encontrado")
	}
	for _, c := range cards {
		if !p.HasCardID(c.ID) {
			return invalidAction("No tienes la carta %s", c.ID)
		}
	}
	return nil
}

func playerMustHaveCard(playerID engine.PlayerID, cardID string, s State) error {
	p, ok := s.PlayerByID(playerID)
	if !ok {
		return gameError("Jugador no encontrado")
	}
	if !p.HasCardID(cardID) {
		return invalidAction("No tienes la carta %s", cardID)
	}
	return nil
}

func cardOrZero(c *Card) Card {
	if c == nil {
		return Card{}
	}
	return *c
}

// CardRules en Dart (rules/card_rules.dart).

// canPlay: las cartas de gato nunca son jugables sueltas (PlayCardAction) —
// solo tienen efecto en pareja/trío. Sin este chequeo, jugar una sola carta
// de gato pasaría la validación, se descartaría sin efecto y el turno no
// avanzaría — un "agujero negro" que en el cliente Flutter solo se evita
// porque la UI deshabilita ese botón; cualquier otro camino (red, bots) lo
// dispararía igual.
func canPlay(c Card, s State) bool {
	player, ok := s.CurrentPlayer()
	if !ok || player.ID != s.Turn.CurrentPlayerID {
		return false
	}
	if s.Turn.Phase != TurnPlaying {
		return false
	}
	if !c.Type.IsPlayable() || c.Type.IsCatCard() {
		return false
	}
	return player.HasCardID(c.ID)
}

func canNope(p Player, s State) bool {
	if s.Turn.Phase != TurnNopeWindow || s.PendingAction == nil {
		return false
	}
	return p.HasCard(CardNope)
}

func canDefuse(p Player) bool { return p.HasCard(CardDefuse) }

func isValidCatPair(cards []Card) bool { return isValidCatGroup(cards, 2) }
func isValidCatTrio(cards []Card) bool { return isValidCatGroup(cards, 3) }

func isValidCatGroup(cards []Card, size int) bool {
	if len(cards) != size || !cards[0].Type.IsCatCard() {
		return false
	}
	for _, c := range cards {
		if c.Type != cards[0].Type {
			return false
		}
	}
	return true
}

// NopeRules en Dart (rules/nope_rules.dart).

func isActionCancelled(s State) bool { return s.Turn.IsNoped() }

func canAddNope(s State) bool {
	return s.PendingAction != nil && s.Turn.Phase == TurnNopeWindow
}

func incrementNopeChain(current int) int { return current + 1 }

// TurnRules en Dart (rules/turn_rules.dart).

func isTurnOver(s State) bool { return s.Turn.Phase == TurnEnded }

// nextPlayerID devuelve el siguiente jugador vivo en sentido horario. Si el
// jugador actual ya no está en la lista de vivos (se acaba de eliminar a sí
// mismo), currentIndex da -1 y (-1+1)%len = 0 → arranca desde el primero,
// igual que el indexWhere de Dart.
func nextPlayerID(s State) (engine.PlayerID, error) {
	alive := s.AlivePlayers()
	if len(alive) == 0 {
		return "", errors.New("explodingkittens: no hay jugadores vivos")
	}
	currentIndex := -1
	for i, p := range alive {
		if p.ID == s.Turn.CurrentPlayerID {
			currentIndex = i
			break
		}
	}
	return alive[(currentIndex+1)%len(alive)].ID, nil
}

func attackTurnsLeft(s State) int {
	if s.Turn.ActionsLeft > 1 {
		return s.Turn.ActionsLeft - 1
	}
	return 0
}
