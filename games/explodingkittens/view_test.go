package explodingkittens

import (
	"testing"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

// pendingCatTrioState arma un State con un trío de gatos ya jugado por p1
// contra p2, resuelto (nadie nopeó) y a la espera de que p1 elija a ciegas —
// el punto exacto en el que HiddenHandIds tiene que aparecer.
func pendingCatTrioState() State {
	return State{
		ID: "test",
		Players: []Player{
			{ID: "p1", Name: "Ana", Status: StatusActive},
			{
				ID:     "p2",
				Name:   "Beto",
				Status: StatusActive,
				Hand:   []Card{{ID: "c1", Type: CardSkip}, {ID: "c2", Type: CardNope}},
			},
			{ID: "p3", Name: "Caro", Status: StatusActive},
		},
		Turn:          Turn{CurrentPlayerID: "p1", Phase: TurnAwaitingCardChoice},
		Phase:         PhasePlaying,
		PendingAction: TurnAction{Type: ActionPlayCatTrio, PlayerID: "p1", TargetPlayerID: "p2"},
	}
}

func playerView(t *testing.T, v View, id engine.PlayerID) *PlayerView {
	t.Helper()
	for i := range v.Players {
		if v.Players[i].ID == id {
			return &v.Players[i]
		}
	}
	t.Fatalf("no se encontró a %s en la View", id)
	return nil
}

func TestViewRevealsHandIDsForPendingBlindCatTrioPick(t *testing.T) {
	s := pendingCatTrioState()

	viewAny, err := (Engine{}).View(s, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p2View := playerView(t, viewAny.(View), "p2")

	if p2View.Hand != nil {
		t.Error("p1 no debería ver el type de las cartas de p2, solo el id")
	}
	want := []string{"c1", "c2"}
	got := p2View.HiddenHandIds
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("HiddenHandIds: esperaba %v, llegó %v", want, got)
	}
}

func TestViewDoesNotRevealHandIDsToOtherPlayers(t *testing.T) {
	s := pendingCatTrioState()

	// p3 no es quien tiene el trío pendiente (es de p1) — no debería recibir
	// nada de la mano de p2, ni siquiera los ids.
	viewAny, err := (Engine{}).View(s, "p3")
	if err != nil {
		t.Fatal(err)
	}
	p2View := playerView(t, viewAny.(View), "p2")
	if p2View.HiddenHandIds != nil {
		t.Errorf("p3 no debería ver HiddenHandIds de p2, llegó %v", p2View.HiddenHandIds)
	}
}

func TestViewDoesNotRevealHandIDsOutsideAwaitingCardChoice(t *testing.T) {
	s := pendingCatTrioState()
	// Mismo trío pendiente, pero todavía en la ventana de Nope (no resuelta) —
	// ahí tampoco debería filtrarse nada.
	s.Turn.Phase = TurnNopeWindow

	viewAny, err := (Engine{}).View(s, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p2View := playerView(t, viewAny.(View), "p2")
	if p2View.HiddenHandIds != nil {
		t.Errorf("no debería revelar ids en nope_window, llegó %v", p2View.HiddenHandIds)
	}
}

// TestBlindCatTrioPickResolvesWithRealCardIDFromView es el camino completo
// que necesita el cliente online (issue exploding_kittens#35, Stage B):
// jugar el trío, resolver la ventana de Nope, leer un id real de la View, y
// mandar ese id de vuelta en choose_card — sin esto, un cliente online no
// tenía ningún id válido que mandar y stealChosenCard nunca encontraba la
// carta (buscaba por ID exacto en la mano del objetivo).
func TestBlindCatTrioPickResolvesWithRealCardIDFromView(t *testing.T) {
	e := Engine{}
	s := State{
		ID: "test",
		Players: []Player{
			{
				ID: "p1", Name: "Ana", Status: StatusActive,
				Hand: []Card{
					{ID: "t1", Type: CardTacocat},
					{ID: "t2", Type: CardTacocat},
					{ID: "t3", Type: CardTacocat},
				},
			},
			{
				ID: "p2", Name: "Beto", Status: StatusActive,
				Hand: []Card{{ID: "skip1", Type: CardSkip}, {ID: "nope1", Type: CardNope}},
			},
		},
		Turn:  Turn{CurrentPlayerID: "p1", Phase: TurnPlaying, ActionsLeft: 1},
		Phase: PhasePlaying,
	}

	trioCards := append([]Card{}, s.Players[0].Hand...)
	afterTrio, _, err := e.Apply(s, TurnAction{
		Type: ActionPlayCatTrio, PlayerID: "p1",
		Cards: trioCards, TargetPlayerID: "p2",
	})
	if err != nil {
		t.Fatalf("jugar el trío debería ser válido: %v", err)
	}

	afterResolve, _, err := e.ResolveNopeWindow(afterTrio)
	if err != nil {
		t.Fatal(err)
	}
	resolved := afterResolve.(State)
	if resolved.Turn.Phase != TurnAwaitingCardChoice {
		t.Fatalf("esperaba awaiting_card_choice, quedó en %s", resolved.Turn.Phase)
	}

	viewAny, err := e.View(afterResolve, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p2View := playerView(t, viewAny.(View), "p2")
	if len(p2View.HiddenHandIds) != 2 {
		t.Fatalf("esperaba 2 ids ocultos de la mano de p2, llegó %+v", p2View.HiddenHandIds)
	}
	chosenID := p2View.HiddenHandIds[0]

	finalAny, _, err := e.Apply(afterResolve, TurnAction{
		Type: ActionChooseCard, PlayerID: "p1", CardID: chosenID,
	})
	if err != nil {
		t.Fatalf("choose_card con un id real sacado de la View debería ser válido: %v", err)
	}
	final := finalAny.(State)

	p1, _ := final.PlayerByID("p1")
	if !p1.HasCardID(chosenID) {
		t.Error("p1 debería tener la carta elegida en su mano tras robarla")
	}
	p2, _ := final.PlayerByID("p2")
	if p2.HasCardID(chosenID) {
		t.Error("p2 no debería seguir teniendo la carta que le robaron")
	}
	if len(p2.Hand) != 1 {
		t.Errorf("p2 debería quedar con 1 carta, tiene %d", len(p2.Hand))
	}
}
