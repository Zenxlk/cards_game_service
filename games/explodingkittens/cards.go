// Package explodingkittens implementa engine.GameEngine para Exploding
// Kittens. Es un puerto fiel de las reglas ya probadas en el cliente Flutter
// (lib/game_engine/), no una reimplementación desde cero — ver el mapeo de
// cada archivo a su origen Dart en los comentarios de cada función.
package explodingkittens

// CardType : lib/game_engine/models/card/card_type.dart
type CardType string

const (
	CardExplodingKitten    CardType = "exploding_kitten"
	CardDefuse             CardType = "defuse"
	CardNope               CardType = "nope"
	CardAttack             CardType = "attack"
	CardSkip               CardType = "skip"
	CardFavor              CardType = "favor"
	CardShuffle            CardType = "shuffle"
	CardSeeTheFuture       CardType = "see_the_future"
	CardTacocat            CardType = "tacocat"
	CardRainbowRalphingCat CardType = "rainbow_ralphing_cat"
	CardBeardedDragon      CardType = "bearded_dragon"
	CardCattermelon        CardType = "cattermelon"
	CardHairyPotatoCat     CardType = "hairy_potato_cat"
)

// IsCatCard : cartas de gato, solo tienen efecto en pareja/trío, nunca sueltas.
func (t CardType) IsCatCard() bool {
	switch t {
	case CardTacocat, CardRainbowRalphingCat, CardBeardedDragon, CardCattermelon, CardHairyPotatoCat:
		return true
	default:
		return false
	}
}

func (t CardType) IsPlayable() bool {
	return t != CardExplodingKitten && t != CardDefuse
}

func (t CardType) RequiresTarget() bool {
	return t == CardFavor
}

// Card : CardModel en Dart.
type Card struct {
	ID   string   `json:"id"`
	Type CardType `json:"type"`
}
