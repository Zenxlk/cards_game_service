package explodingkittens

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ZenXLK/cards-game-service/pkg/engine"
)

func twoPlayers() []engine.PlayerInfo {
	return []engine.PlayerInfo{
		{ID: "p1", Name: "Ana"},
		{ID: "p2", Name: "Beto"},
	}
}

func TestStartRejectsPlayerCountOutOfRange(t *testing.T) {
	e := Engine{}
	if _, err := e.Start([]engine.PlayerInfo{{ID: "solo", Name: "Solo"}}, nil); err == nil {
		t.Fatal("se esperaba error con 1 jugador")
	}

	six := make([]engine.PlayerInfo, 6)
	for i := range six {
		six[i] = engine.PlayerInfo{ID: engine.PlayerID(rune('a' + i))}
	}
	if _, err := e.Start(six, nil); err == nil {
		t.Fatal("se esperaba error con 6 jugadores")
	}
}

// TestStartIsDeterministicWithSeed prueba que Start es una función pura: con
// la misma semilla, dos partidas independientes deben quedar bit a bit
// idénticas. Es la propiedad que hace posible testear el motor sin mocks.
func TestStartIsDeterministicWithSeed(t *testing.T) {
	e := Engine{}
	seed := int64(42)
	cfg, _ := json.Marshal(Config{Seed: &seed})

	s1, err := e.Start(twoPlayers(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := e.Start(twoPlayers(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	j1, _ := json.Marshal(s1.(State).Players)
	j2, _ := json.Marshal(s2.(State).Players)
	if string(j1) != string(j2) {
		t.Fatalf("dos partidas con la misma seed deberían repartir igual:\n%s\n%s", j1, j2)
	}
}

func TestStartDealsCorrectHandSizesAndBombCount(t *testing.T) {
	e := Engine{}
	seed := int64(7)
	cfg, _ := json.Marshal(Config{Seed: &seed})
	state, err := e.Start(twoPlayers(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := state.(State)

	for _, p := range s.Players {
		if len(p.Hand) != InitialHandSize+1 { // +1 por el Defuse repartido a cada jugador
			t.Errorf("jugador %s: esperaba %d cartas, tiene %d", p.ID, InitialHandSize+1, len(p.Hand))
		}
	}

	bombs := 0
	for _, c := range s.Deck.DrawPile {
		if c.Type == CardExplodingKitten {
			bombs++
		}
	}
	if want := len(s.Players) - 1; bombs != want {
		t.Errorf("esperaba %d exploding kittens en el mazo, hay %d", want, bombs)
	}
}

func TestApplyRejectsActionFromWrongPlayer(t *testing.T) {
	e := Engine{}
	state, err := e.Start(twoPlayers(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := state.(State)
	notCurrent := s.Players[0].ID
	if notCurrent == s.Turn.CurrentPlayerID {
		notCurrent = s.Players[1].ID
	}

	_, _, err = e.Apply(state, TurnAction{Type: ActionDrawCard, PlayerID: notCurrent})
	if err == nil {
		t.Fatal("se esperaba error: no es su turno")
	}
	var invalidErr *InvalidActionError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("se esperaba *InvalidActionError, llegó %T: %v", err, err)
	}
}

// TestDrawCardAdvancesTurn arma un mazo determinista a mano (sin pasar por
// buildDeck) para no depender del azar: robar una carta que no es bomba
// debe sumarla a la mano y pasar el turno al siguiente jugador.
func TestDrawCardAdvancesTurn(t *testing.T) {
	s := State{
		ID: "test",
		Players: []Player{
			{ID: "p1", Name: "Ana", Status: StatusActive},
			{ID: "p2", Name: "Beto", Status: StatusActive},
		},
		Deck:  Deck{DrawPile: []Card{{ID: "skip_0", Type: CardSkip}}},
		Turn:  Turn{CurrentPlayerID: "p1", Phase: TurnPlaying, ActionsLeft: 1},
		Phase: PhasePlaying,
	}

	next, events, err := (Engine{}).Apply(s, TurnAction{Type: ActionDrawCard, PlayerID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	ns := next.(State)

	if ns.Turn.CurrentPlayerID != "p2" {
		t.Errorf("esperaba que el turno pasara a p2, quedó en %s", ns.Turn.CurrentPlayerID)
	}
	p1, _ := ns.PlayerByID("p1")
	if len(p1.Hand) != 1 || p1.Hand[0].Type != CardSkip {
		t.Errorf("esperaba la carta Skip en la mano de p1, tiene %+v", p1.Hand)
	}
	if len(events) == 0 {
		t.Error("esperaba al menos un evento (card_drawn)")
	}
}

// TestDrawExplodingKittenWithoutDefuseEliminatesPlayer cubre el camino
// completo: bomba sin Defuse -> eliminación -> WinCondition -> fin de
// partida, cuando solo quedan dos jugadores.
func TestDrawExplodingKittenWithoutDefuseEliminatesPlayer(t *testing.T) {
	s := State{
		ID: "test",
		Players: []Player{
			{ID: "p1", Name: "Ana", Status: StatusActive}, // sin Defuse en mano
			{ID: "p2", Name: "Beto", Status: StatusActive},
		},
		Deck:  Deck{DrawPile: []Card{{ID: "bomb_0", Type: CardExplodingKitten}}},
		Turn:  Turn{CurrentPlayerID: "p1", Phase: TurnPlaying, ActionsLeft: 1},
		Phase: PhasePlaying,
	}

	next, events, err := (Engine{}).Apply(s, TurnAction{Type: ActionDrawCard, PlayerID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	ns := next.(State)

	if ns.Phase != PhaseFinished {
		t.Fatalf("esperaba partida terminada, fase quedó en %s", ns.Phase)
	}
	if ns.Result == nil || ns.Result.WinnerID != "p2" {
		t.Fatalf("esperaba a p2 como ganador, resultado: %+v", ns.Result)
	}

	var sawGameOver bool
	for _, e := range events {
		if e.Type == EventGameOver {
			sawGameOver = true
		}
	}
	if !sawGameOver {
		t.Error("esperaba un evento game_over")
	}
}

func TestViewHidesOtherPlayersHands(t *testing.T) {
	e := Engine{}
	state, err := e.Start(twoPlayers(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := state.(State)

	viewAny, err := e.View(state, "p1")
	if err != nil {
		t.Fatal(err)
	}
	v := viewAny.(View)

	for _, pv := range v.Players {
		if pv.ID == "p1" {
			if len(pv.Hand) != InitialHandSize+1 {
				t.Errorf("p1 debería ver su propia mano completa, vio %d cartas", len(pv.Hand))
			}
			continue
		}
		if pv.Hand != nil {
			t.Errorf("p1 no debería ver la mano de %s", pv.ID)
		}
		other, _ := s.PlayerByID(pv.ID)
		if pv.HandSize != len(other.Hand) {
			t.Errorf("handSize de %s: esperaba %d, llegó %d", pv.ID, len(other.Hand), pv.HandSize)
		}
	}
}
