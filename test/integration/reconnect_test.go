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

// newTestServer levanta un lobby + servidor HTTP real para los tests de
// este archivo — misma configuración que gameplay_test.go, factorizada acá
// porque cada test necesita su propia sala.
func newTestServer(t *testing.T, maxPlayers int, grace time.Duration) (wsURL string) {
	t.Helper()
	l := lobby.New(lobby.Config{
		CodeLength: config.Default().RoomCodeLength,
		Room:       room.Config{MaxPlayers: maxPlayers, GraceDuration: grace},
	})
	srv := transport.NewServer(context.Background(), l, transport.DefaultConfig())
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

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
	return "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/" + created.Code
}

func dialClient(t *testing.T, wsURL string) *client {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close(websocket.StatusNormalClosure, "") })
	return &client{t: t, ws: ws}
}

// TestSessionTokenIssuedOnFirstJoin: el primer join_room de un playerId
// nuevo (host incluido) recibe un session_token, y room_state ya trae
// maxPlayers (bug confirmado contra el modelo real del cliente Flutter,
// LobbyRoom.fromJson espera ese campo).
func TestSessionTokenIssuedOnFirstJoin(t *testing.T) {
	wsURL := newTestServer(t, 5, time.Second)

	p1 := dialClient(t, wsURL)
	p1.send("join_room", map[string]any{"playerId": "p1", "name": "Ana"})

	tok := p1.readUntil("session_token")
	token, _ := tok["token"].(string)
	if token == "" {
		t.Fatal("se esperaba un session_token no vacío en el primer join")
	}

	roomState := p1.readUntil("room_state")
	roomPayload, _ := roomState["room"].(map[string]any)
	if roomPayload["maxPlayers"] != float64(5) {
		t.Fatalf("se esperaba maxPlayers=5 en room_state, llegó %v", roomPayload["maxPlayers"])
	}

	p2 := dialClient(t, wsURL)
	p2.send("join_room", map[string]any{"playerId": "p2", "name": "Beto"})
	p2Tok := p2.readUntil("session_token")
	if p2Tok["token"] == token {
		t.Fatal("cada jugador debe recibir un token distinto")
	}
}

// TestReconnectRequiresValidToken: una vez que un jugador quedó activo en
// partida y su conexión se cae, solo puede recuperar su lugar con el token
// que le fue emitido — sin token o con uno incorrecto, la reconexión se
// rechaza y no reemplaza al jugador.
func TestReconnectRequiresValidToken(t *testing.T) {
	wsURL := newTestServer(t, 5, 5*time.Second) // grace largo: no queremos que EliminateForDisconnect dispare durante el test

	p1 := dialClient(t, wsURL)
	p1.send("join_room", map[string]any{"playerId": "p1", "name": "Ana"})
	tok := p1.readUntil("session_token")
	token, _ := tok["token"].(string)
	p1.readUntil("room_state")

	p2 := dialClient(t, wsURL)
	p2.send("join_room", map[string]any{"playerId": "p2", "name": "Beto"})
	p2.readUntil("session_token")
	p2.readUntil("room_state")
	p1.readUntil("room_state") // p1 también ve a p2 sumarse

	p2.send("set_ready", map[string]any{"ready": true})
	p1.readUntil("room_state")
	p2.readUntil("room_state")

	p1.send("start_game", nil)
	p1.readUntil("game_starting")
	p2.readUntil("game_starting")
	p1.readUntil("game_state")
	p2.readUntil("game_state")

	// p1 se desconecta a mitad de partida (fase activa: el room arranca el
	// grace period en vez de sacarlo del lobby).
	if err := p1.ws.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}

	// Reconexión sin token: rechazada.
	badNoToken := dialClient(t, wsURL)
	badNoToken.send("join_room", map[string]any{"playerId": "p1", "name": "Ana"})
	errFrame := badNoToken.readUntil("ws_error")
	if errFrame["message"] != "Token de sesión inválido" {
		t.Fatalf("mensaje de error inesperado: %v", errFrame["message"])
	}

	// Reconexión con token incorrecto: rechazada.
	badWrongToken := dialClient(t, wsURL)
	badWrongToken.send("join_room", map[string]any{"playerId": "p1", "name": "Ana", "token": token + "x"})
	errFrame2 := badWrongToken.readUntil("ws_error")
	if errFrame2["message"] != "Token de sesión inválido" {
		t.Fatalf("mensaje de error inesperado: %v", errFrame2["message"])
	}

	// Reconexión con el token correcto: aceptada, recupera su lugar.
	good := dialClient(t, wsURL)
	good.send("join_room", map[string]any{"playerId": "p1", "name": "Ana", "token": token})
	roomState := good.readUntil("room_state")
	roomPayload, _ := roomState["room"].(map[string]any)
	if roomPayload["hostId"] != "p1" {
		t.Fatalf("se esperaba reconectar como p1, room_state: %v", roomState)
	}
}
