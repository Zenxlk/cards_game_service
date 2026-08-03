// Excepción deliberada al criterio general de test/integration (usar
// games/fixture, no un juego real, ver gameplay_test.go): acá el objetivo
// es justamente probar la interacción entre la reconexión genérica de
// internal/room y los hooks concretos de un motor real
// (MarkPlayerDisconnected/MarkPlayerReconnected de explodingkittens, que sí
// tocan Player.Status) — algo que fixture no puede validar porque sus
// hooks no hacen nada con el estado.
package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ZenXLK/cards_game_service/internal/config"
	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/internal/transport"

	_ "github.com/ZenXLK/cards_game_service/games/explodingkittens"
)

func TestReconnectMidGame_RealExplodingKittensEngine(t *testing.T) {
	l := lobby.New(lobby.Config{
		CodeLength: config.Default().RoomCodeLength,
		Room:       room.Config{MaxPlayers: 5, GraceDuration: 5 * time.Second},
	})
	srv := transport.NewServer(context.Background(), l, transport.DefaultConfig())
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	createBody := `{"gameType":"exploding_kittens","hostId":"p1","hostName":"Ana"}`
	resp, err := httpSrv.Client().Post(httpSrv.URL+"/rooms", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/" + created.Code

	dial := func() *client {
		ws, _, err := websocket.Dial(context.Background(), wsURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close(websocket.StatusNormalClosure, "") })
		return &client{t: t, ws: ws}
	}

	p1 := dial()
	p1.send("join_room", map[string]any{"playerId": "p1", "name": "Ana"})
	p1.readUntil("session_token")
	p1.readUntil("room_state")

	p2 := dial()
	p2.send("join_room", map[string]any{"playerId": "p2", "name": "Beto"})
	p2tok := p2.readUntil("session_token")
	p2Token, _ := p2tok["token"].(string)
	p2.readUntil("room_state")
	p1.readUntil("room_state")

	p2.send("set_ready", map[string]any{"ready": true})
	p1.readUntil("room_state")
	p2.readUntil("room_state")

	p1.send("start_game", nil)
	p1.readUntil("game_starting")
	p2.readUntil("game_starting")
	p1.readUntil("game_state")
	p2.readUntil("game_state")

	// p2 se cae a mitad de partida.
	if err := p2.ws.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}

	disc := p1.readUntil("player_disconnected")
	if disc["playerId"] != "p2" {
		t.Fatalf("player_disconnected: playerId inesperado %v", disc["playerId"])
	}

	// El motor real refleja la desconexión en su propio modelo de estado,
	// no solo en el evento genérico — Player.Status pasa a "disconnected"
	// para p2 en la vista de p1.
	stateAfterDisconnect := p1.readUntil("game_state")
	if !playerHasStatus(t, stateAfterDisconnect, "p2", "disconnected") {
		t.Fatalf("esperaba p2 con status=disconnected tras la desconexión: %v", stateAfterDisconnect)
	}

	// Reconecta con el token correcto.
	p2b := dial()
	p2b.send("join_room", map[string]any{"playerId": "p2", "name": "Beto", "token": p2Token})
	p2b.readUntil("room_state")

	reconn := p1.readUntil("player_reconnected")
	if reconn["playerId"] != "p2" {
		t.Fatalf("player_reconnected: playerId inesperado %v", reconn["playerId"])
	}

	// Tanto p1 como el propio p2 reconectado deben recibir un game_state al
	// día — la reconexión no debe dejar a nadie con un estado viejo.
	stateAfterReconnect := p1.readUntil("game_state")
	if !playerHasStatus(t, stateAfterReconnect, "p2", "active") {
		t.Fatalf("esperaba p2 con status=active tras reconectar: %v", stateAfterReconnect)
	}

	p2bState := p2b.readUntil("game_state")
	payload, ok := p2bState["payload"].(map[string]any)
	if !ok {
		t.Fatalf("p2 reconectado debería recibir un game_state válido: %v", p2bState)
	}
	players, _ := payload["players"].([]any)
	if len(players) != 2 {
		t.Fatalf("esperaba 2 jugadores en el game_state de p2 reconectado, hay %d", len(players))
	}
}

func playerHasStatus(t *testing.T, gameState map[string]any, playerID, wantStatus string) bool {
	t.Helper()
	payload, ok := gameState["payload"].(map[string]any)
	if !ok {
		t.Fatalf("game_state sin payload: %v", gameState)
	}
	players, ok := payload["players"].([]any)
	if !ok {
		t.Fatalf("game_state sin lista de players: %v", payload)
	}
	for _, p := range players {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pm["id"] == playerID {
			return pm["status"] == wantStatus
		}
	}
	return false
}
