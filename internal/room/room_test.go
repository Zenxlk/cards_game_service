package room

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

// panickyEngine simula un bug del motor (panic dentro de Apply) para probar
// que safeExec lo contiene sin tirar el proceso — ver el comentario en
// Room.safeExec sobre por qué esto importa: sin recover(), un panic en
// cualquier sala mata TODAS las salas del proceso.
func init() {
	engine.Register("room_test_panicky", func(json.RawMessage) (engine.GameEngine, error) {
		return panickyEngine{}, nil
	})
}

type panickyState struct{ Players []engine.PlayerInfo }

func (panickyState) Terminal() bool { return false }

type panickyEngine struct{}

func (panickyEngine) Start(players []engine.PlayerInfo, _ json.RawMessage) (engine.State, error) {
	return panickyState{Players: players}, nil
}

func (panickyEngine) DecodeAction(_ engine.PlayerID, _ json.RawMessage) (engine.Action, error) {
	return struct{}{}, nil
}

func (panickyEngine) Apply(engine.State, engine.Action) (engine.State, []engine.Event, error) {
	panic("boom: bug simulado del motor")
}

func (panickyEngine) ResolveNopeWindow(s engine.State) (engine.State, []engine.Event, error) {
	return s, nil, nil
}

func (panickyEngine) MarkPlayerDisconnected(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}

func (panickyEngine) MarkPlayerReconnected(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}

func (panickyEngine) EliminateForDisconnect(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}

func (panickyEngine) View(s engine.State, _ engine.PlayerID) (any, error) { return s, nil }

func (panickyEngine) PendingTimer(engine.State) (time.Duration, bool) { return 0, false }

func joinFrame(playerID, name string) Frame {
	raw, _ := json.Marshal(map[string]string{"playerId": playerID, "name": name})
	return Frame{Type: "join_room", Raw: raw}
}

func drainUntil(t *testing.T, c *Conn, msgType string) Frame {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f, ok := <-c.Out():
			if !ok {
				t.Fatalf("canal cerrado esperando %q", msgType)
			}
			if f.Type == msgType {
				return f
			}
		case <-deadline:
			t.Fatalf("timeout esperando %q", msgType)
		}
	}
}

func TestOnLobbyIdleExpired_ClosesRoomWhenNoGameStarts(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM1", "room_test_panicky", host, Config{MaxPlayers: 2, LobbyIdleTimeout: 20 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	c := r.Connect()
	r.HandleMessage(c, joinFrame("p1", "Ana"))

	drainUntil(t, c, "player_kicked")

	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("la sala no terminó su goroutine tras cerrarse por inactividad")
	}
}

func TestOnLobbyIdleExpired_DoesNothingIfGameAlreadyStarted(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM2", "room_test_panicky", host, Config{MaxPlayers: 2, LobbyIdleTimeout: 20 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrame("p1", "Ana"))
	drainUntil(t, hostConn, "room_state")

	guestConn := r.Connect()
	r.HandleMessage(guestConn, joinFrame("p2", "Beto"))
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, Frame{Type: "set_ready", Raw: json.RawMessage(`{"ready":true}`)})
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(hostConn, Frame{Type: "start_game"})
	drainUntil(t, hostConn, "game_starting")

	// El timer de inactividad (20ms) ya venció, pero como la partida arrancó
	// (cancelLobbyIdle en onStartGame) no debe cerrar la sala.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-r.Done():
		t.Fatal("la sala se cerró por inactividad pese a que la partida ya había arrancado")
	default:
	}
}

func TestSafeExec_RecoversPanicAndClosesRoomWithoutCrashingProcess(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM3", "room_test_panicky", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrame("p1", "Ana"))
	drainUntil(t, hostConn, "room_state")

	guestConn := r.Connect()
	r.HandleMessage(guestConn, joinFrame("p2", "Beto"))
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, Frame{Type: "set_ready", Raw: json.RawMessage(`{"ready":true}`)})
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(hostConn, Frame{Type: "start_game"})
	drainUntil(t, hostConn, "game_starting")

	// panickyEngine.Apply panica a propósito acá — si safeExec no lo
	// recuperara, este panic tiraría todo el binario de test.
	r.HandleMessage(hostConn, Frame{Type: "action", Raw: json.RawMessage(`{"payload":{}}`)})

	drainUntil(t, hostConn, "player_kicked")

	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("la sala no cerró su goroutine tras el panic recuperado")
	}
}

func TestOnJoin_RejectsOversizedPlayerIDOrName(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM4", "room_test_panicky", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go r.Run(ctx)

	c := r.Connect()
	tooLongID := strings.Repeat("x", MaxPlayerIDLen+1)
	r.HandleMessage(c, joinFrame(tooLongID, "Ana"))

	f := drainUntil(t, c, "ws_error")
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(f.Raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message == "" {
		t.Fatal("se esperaba un mensaje de error en ws_error")
	}
}
