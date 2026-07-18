package explodingkittens

import (
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// buildDeck : DeckBuilder.build en Dart (deck/deck_builder.dart). Con
// cfg.Seed fijo es determinista — usado por los tests para armar partidas
// reproducibles sin mocks. El contador de IDs de carta (nextID) es una
// variable local a esta llamada, nunca de paquete: en Dart es estática
// porque un proceso solo corre una partida a la vez (LAN); acá muchas salas
// concurrentes comparten el mismo proceso, así que un contador global
// correría una data race entre las goroutines de distintos rooms.
func buildDeck(players []Player, cfg Config) (Deck, []Player) {
	seed := time.Now().UnixNano()
	if cfg.Seed != nil {
		seed = *cfg.Seed
	}
	rng := rand.New(rand.NewSource(seed))

	nextID := 0
	makeCard := func(t CardType) Card {
		c := Card{ID: fmt.Sprintf("%s_%d", t, nextID), Type: t}
		nextID++
		return c
	}
	repeat := func(t CardType, n int) []Card {
		cs := make([]Card, n)
		for i := range cs {
			cs[i] = makeCard(t)
		}
		return cs
	}

	n := len(players)

	// 1. Cartas base sin Defuse ni Exploding Kittens.
	cards := make([]Card, 0, 64)
	cards = append(cards, repeat(CardNope, NopeCount)...)
	cards = append(cards, repeat(CardAttack, AttackCount)...)
	cards = append(cards, repeat(CardSkip, SkipCount)...)
	cards = append(cards, repeat(CardFavor, FavorCount)...)
	cards = append(cards, repeat(CardShuffle, ShuffleCount)...)
	cards = append(cards, repeat(CardSeeTheFuture, SeeTheFutureCount)...)
	cards = append(cards, repeat(CardTacocat, CatCardCount)...)
	cards = append(cards, repeat(CardRainbowRalphingCat, CatCardCount)...)
	cards = append(cards, repeat(CardBeardedDragon, CatCardCount)...)
	cards = append(cards, repeat(CardCattermelon, CatCardCount)...)
	cards = append(cards, repeat(CardHairyPotatoCat, CatCardCount)...)
	rng.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })

	// 2. Repartir mano inicial (sin Defuse) + 1 Defuse por jugador.
	updatedPlayers := make([]Player, 0, n)
	remaining := cards
	for _, p := range players {
		take := InitialHandSize
		if take > len(remaining) {
			take = len(remaining)
		}
		hand := append([]Card{}, remaining[:take]...)
		remaining = remaining[take:]
		hand = append(hand, makeCard(CardDefuse))

		p.Hand = hand
		p.Status = StatusActive
		updatedPlayers = append(updatedPlayers, p)
	}

	// 3. Exploding Kittens (n-1) + 2 Defuses extra, todo mezclado al mazo.
	bombs := repeat(CardExplodingKitten, n-1)
	extraDefuses := repeat(CardDefuse, 2)

	drawPile := make([]Card, 0, len(remaining)+len(bombs)+len(extraDefuses))
	drawPile = append(drawPile, remaining...)
	drawPile = append(drawPile, bombs...)
	drawPile = append(drawPile, extraDefuses...)
	rng.Shuffle(len(drawPile), func(i, j int) { drawPile[i], drawPile[j] = drawPile[j], drawPile[i] })

	return Deck{DrawPile: drawPile, DiscardPile: []Card{}}, updatedPlayers
}

// El resto : DeckManager en Dart (deck/deck_manager.dart).

func drawTop(d Deck) (Deck, Card, error) {
	if len(d.DrawPile) == 0 {
		return Deck{}, Card{}, errors.New("explodingkittens: el mazo está vacío")
	}
	drawn := d.DrawPile[0]
	rest := append([]Card{}, d.DrawPile[1:]...)
	return Deck{DrawPile: rest, DiscardPile: d.DiscardPile}, drawn, nil
}

// insertAt inserta card en position (recortado a [0, len(pile)], igual que
// int.clamp en Dart). Reinserta la bomba exacta que se robó tras un Defuse.
func insertAt(d Deck, card Card, position int) Deck {
	pile := append([]Card{}, d.DrawPile...)
	if position < 0 {
		position = 0
	}
	if position > len(pile) {
		position = len(pile)
	}
	pile = append(pile, Card{})
	copy(pile[position+1:], pile[position:])
	pile[position] = card
	return Deck{DrawPile: pile, DiscardPile: d.DiscardPile}
}

func discardCard(d Deck, card Card) Deck {
	return Deck{DrawPile: d.DrawPile, DiscardPile: append(append([]Card{}, d.DiscardPile...), card)}
}

func shuffleDeck(d Deck, rng *rand.Rand) Deck {
	pile := append([]Card{}, d.DrawPile...)
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	rng.Shuffle(len(pile), func(i, j int) { pile[i], pile[j] = pile[j], pile[i] })
	return Deck{DrawPile: pile, DiscardPile: d.DiscardPile}
}

func peekTop(d Deck, n int) []Card {
	if n > len(d.DrawPile) {
		n = len(d.DrawPile)
	}
	return append([]Card{}, d.DrawPile[:n]...)
}
