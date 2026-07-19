// Package integration prueba room+transport de punta a punta: servidor
// HTTP real (httptest), clientes WebSocket reales — nada de mocks. Usa
// games/fixture, no explodingkittens, para que un bug de reglas de un juego
// concreto no pueda romper un test que en realidad prueba transporte y
// ciclo de vida de sala.
package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/ZenXLK/cards_game_service/internal/config"
	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/internal/transport"

	_ "github.com/ZenXLK/cards_game_service/games/fixture"
)

type client struct {
	t  *testing.T
	ws *websocket.Conn
}

func (c *client) send(msgType string, fields map[string]any) {
	m := map[string]any{"type": msgType}
	for k, v := range fields {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

// readUntil descarta frames hasta encontrar uno de wantType, con timeout —
// evita que el test dependa de la posición exacta de cada broadcast.
func (c *client) readUntil(wantType string) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			c.t.Fatalf("esperando %q: %v", wantType, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			c.t.Fatalf("frame inválido: %v", err)
		}
		if m["type"] == wantType {
			return m
		}
	}
	c.t.Fatalf("timeout esperando un frame de type=%q", wantType)
	return nil
}

func TestFullGameplayOverRealWebSocket(t *testing.T) {
	l := lobby.New(lobby.Config{
		CodeLength: config.Default().RoomCodeLength,
		Room:       room.Config{MaxPlayers: 5, GraceDuration: 2 * time.Second},
	})
	srv := transport.NewServer(context.Background(), l, transport.DefaultConfig())
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	// Crear la sala vía la API REST mínima de lobby.
	createBody := `{"gameType":"fixture","hostId":"p1","hostName":"Ana"}`
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
	if created.Code == "" {
		t.Fatal("se esperaba un código de sala")
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
	p1.readUntil("room_state")

	p2 := dial()
	p2.send("join_room", map[string]any{"playerId": "p2", "name": "Beto"})
	p2.readUntil("room_state")
	// p1 también ve el room_state actualizado con p2 adentro.
	p1.readUntil("room_state")

	p2.send("set_ready", map[string]any{"ready": true})
	p1.readUntil("room_state")
	p2.readUntil("room_state")

	// Solo el host puede arrancar la partida.
	p2.send("start_game", nil)
	rejected := p2.readUntil("ws_error")
	if rejected["message"] != "Solo el host puede empezar la partida" {
		t.Errorf("mensaje de error inesperado: %v", rejected["message"])
	}

	p1.send("start_game", nil)
	p1.readUntil("game_starting")
	p2.readUntil("game_starting")
	state1 := p1.readUntil("game_state")
	p2.readUntil("game_state")

	payload, ok := state1["payload"].(map[string]any)
	if !ok {
		t.Fatalf("game_state sin payload: %v", state1)
	}
	if payload["currentPlayerId"] != "p1" {
		t.Fatalf("se esperaba que p1 empezara, currentPlayerId=%v", payload["currentPlayerId"])
	}

	// p1 juega su turno; ambos deben ver el evento y el estado actualizado
	// con el turno pasado a p2.
	p1.send("action", map[string]any{"payload": map[string]any{"points": 5}})
	ev := p1.readUntil("game_event")
	evInner, _ := ev["payload"].(map[string]any)
	if evInner["type"] != "scored" {
		t.Errorf("se esperaba un evento 'scored', llegó: %v", evInner["type"])
	}
	p2.readUntil("game_event")

	next1 := p1.readUntil("game_state")
	nextPayload, _ := next1["payload"].(map[string]any)
	if nextPayload["currentPlayerId"] != "p2" {
		t.Fatalf("se esperaba que el turno pasara a p2, quedó en %v", nextPayload["currentPlayerId"])
	}
}
