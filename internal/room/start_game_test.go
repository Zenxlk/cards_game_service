package room

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

// TestOnStartGame_BroadcastsRoomStateWithStartingStatus cubre el bug
// reportado en exploding_kittens (lobby online trabado hasta retogglear
// Ready): sin un room_state con status:"starting" tras start_game,
// LobbyScreen (lado Flutter) nunca navega a la partida por su cuenta.
func TestOnStartGame_BroadcastsRoomStateWithStartingStatus(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_START1", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()

	r.HandleMessage(hostConn, joinFrame("host", "Ana"))
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, joinFrame("p2", "Beto"))
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, Frame{Type: "set_ready", Raw: json.RawMessage(`{"ready":true}`)})
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(hostConn, Frame{Type: "start_game"})

	for _, c := range []*Conn{hostConn, guestConn} {
		frame := drainUntil(t, c, "room_state")
		var snapshot LobbySnapshot
		if err := json.Unmarshal(frame.Raw, &snapshot); err != nil {
			t.Fatalf("no se pudo decodificar el room_state: %v", err)
		}
		if snapshot.Status != LobbyStarting {
			t.Fatalf("esperaba status %q tras start_game, quedó %q", LobbyStarting, snapshot.Status)
		}
	}
}

// TestOnStartGame_Rematch_BroadcastsRoomStateWithStartingStatus verifica el
// mismo fix para la revancha: mismo onStartGame, mismo gap.
func TestOnStartGame_Rematch_BroadcastsRoomStateWithStartingStatus(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_START2", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()
	startAndFinishMatch(t, r, hostConn, guestConn)

	r.HandleMessage(hostConn, Frame{Type: "start_game"})

	for _, c := range []*Conn{hostConn, guestConn} {
		frame := drainUntil(t, c, "room_state")
		var snapshot LobbySnapshot
		if err := json.Unmarshal(frame.Raw, &snapshot); err != nil {
			t.Fatalf("no se pudo decodificar el room_state: %v", err)
		}
		if snapshot.Status != LobbyStarting {
			t.Fatalf("esperaba status %q tras la revancha, quedó %q", LobbyStarting, snapshot.Status)
		}
	}
}
