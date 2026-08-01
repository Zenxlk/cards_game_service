package room

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

func joinFrameWithToken(playerID, name, token string) Frame {
	raw, _ := json.Marshal(map[string]string{"playerId": playerID, "name": name, "token": token})
	return Frame{Type: "join_room", Raw: raw}
}

// sessionTokenPayload es el shape exacto que manda issueToken() en room.go
// (map[string]string{"token": ...}) — se decodifica así en vez de a mano
// para no duplicar ese contrato en cada test que necesite reconectar.
type sessionTokenPayload struct {
	Token string `json:"token"`
}

// startAndFinishMatch juega una partida completa contra room_test_terminable
// (registrado en room_test.go): arranca, y la primera "action" del host la
// termina y lo declara ganador. Deja la sala en phaseFinished antes de que
// el test específico de revancha tome el control. Devuelve el token de
// sesión del host — reconectar (probar que el disconnect post-partida no
// lo expulsa) lo necesita, ver docs/TOKENS.md.
func startAndFinishMatch(t *testing.T, r *Room, hostConn, guestConn *Conn) string {
	t.Helper()

	r.HandleMessage(hostConn, joinFrame("host", "Ana"))
	tokenFrame := drainUntil(t, hostConn, "session_token")
	var payload sessionTokenPayload
	if err := json.Unmarshal(tokenFrame.Raw, &payload); err != nil {
		t.Fatalf("no se pudo decodificar el session_token del host: %v", err)
	}
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, joinFrame("p2", "Beto"))
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, Frame{Type: "set_ready", Raw: json.RawMessage(`{"ready":true}`)})
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(hostConn, Frame{Type: "start_game"})
	drainUntil(t, hostConn, "game_starting")
	drainUntil(t, guestConn, "game_starting")

	r.HandleMessage(hostConn, Frame{Type: "action", Raw: json.RawMessage(`{"payload":{}}`)})
	drainUntil(t, hostConn, "game_state")
	drainUntil(t, guestConn, "game_state")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseFinished {
			t.Fatalf("esperaba phaseFinished antes de probar la revancha, quedó %v", r.phase)
		}
	})

	return payload.Token
}

// TestOnStartGame_RematchStartsNewMatchWithSameRosterAndFinalizesIndependently
// es el camino completo de la issue de revancha (exploding_kittens#35,
// Stage C): terminada una partida, el mismo start_game de siempre — no un
// mensaje nuevo — tiene que poder arrancar una segunda con el mismo roster,
// y esa segunda partida tiene que poder cerrarse y registrarse por su
// cuenta (matchFinalized reseteado, no atascado en true por la primera).
func TestOnStartGame_RematchStartsNewMatchWithSameRosterAndFinalizesIndependently(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_REMATCH1", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()
	startAndFinishMatch(t, r, hostConn, guestConn)

	var firstMatchID string
	syncRoom(t, r, func(r *Room) {
		firstMatchID = r.matchID
		if firstMatchID == "" {
			t.Fatal("esperaba un matchID asignado tras la primera partida")
		}
	})

	// Revancha: mismo mensaje start_game de siempre, sala en phaseFinished.
	r.HandleMessage(hostConn, Frame{Type: "start_game"})
	drainUntil(t, hostConn, "game_starting")
	drainUntil(t, guestConn, "game_starting")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseActive {
			t.Fatalf("esperaba phaseActive tras la revancha, quedó %v", r.phase)
		}
		if r.matchFinalized {
			t.Error("matchFinalized debería resetearse a false para la nueva partida")
		}
		if r.matchID == firstMatchID {
			t.Error("esperaba un matchID nuevo para la revancha, no el mismo de la partida anterior")
		}
		if len(r.lobby) != 2 {
			t.Fatalf("esperaba conservar los 2 jugadores del roster, quedaron %d", len(r.lobby))
		}
	})

	// La segunda partida también tiene que poder terminar y registrarse —
	// no silenciosamente no-opear por el flag que dejó la primera.
	r.HandleMessage(guestConn, Frame{Type: "action", Raw: json.RawMessage(`{"payload":{}}`)})
	drainUntil(t, hostConn, "game_state")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseFinished {
			t.Fatalf("esperaba phaseFinished tras terminar la revancha, quedó %v", r.phase)
		}
		if !r.matchFinalized {
			t.Error("esperaba matchFinalized=true tras terminar la revancha")
		}
		w, ok := r.state.(engine.Winner)
		if !ok {
			t.Fatal("terminableState debería implementar engine.Winner")
		}
		winnerID, hasWinner := w.Winner()
		if !hasWinner || winnerID != "p2" {
			t.Fatalf("ganador de la revancha inesperado: %v (hasWinner=%v)", winnerID, hasWinner)
		}
	})
}

func TestOnStartGame_RejectsRematchFromNonHost(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_REMATCH2", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()
	startAndFinishMatch(t, r, hostConn, guestConn)

	r.HandleMessage(guestConn, Frame{Type: "start_game"})
	drainUntil(t, guestConn, "ws_error")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseFinished {
			t.Fatalf("un no-host no debería poder arrancar la revancha; fase quedó %v", r.phase)
		}
	})
}

func TestOnDisconnect_AfterFinishedDoesNotRemovePlayerFromLobby(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_REMATCH3", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()
	startAndFinishMatch(t, r, hostConn, guestConn)

	// A diferencia de un disconnect en phaseActive (grace period) o en
	// phaseWaiting (onLeave lo saca de la sala), acá no debería pasar
	// ninguna de las dos cosas: puede estar esperando la revancha.
	r.HandleDisconnect(guestConn)

	syncRoom(t, r, func(r *Room) {
		if len(r.lobby) != 2 {
			t.Fatalf("el jugador desconectado no debería salir de r.lobby tras terminar la partida, quedaron %d", len(r.lobby))
		}
		if _, ok := r.clients["p2"]; ok {
			t.Error("la conexión debería liberarse igual, aunque el jugador siga en el roster")
		}
	})
}

func TestOnDisconnect_HostAfterFinishedDoesNotCloseRoom(t *testing.T) {
	host := engine.PlayerInfo{ID: "host", Name: "Ana"}
	r := New("ROOM_REMATCH4", "room_test_terminable", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	guestConn := r.Connect()
	hostToken := startAndFinishMatch(t, r, hostConn, guestConn)

	// Si esto disparara el mismo camino que onLeave, cerraría la sala entera
	// (player_kicked a todos) — nadie podría pedir la revancha nunca más.
	r.HandleDisconnect(hostConn)

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseFinished {
			t.Fatalf("la sala no debería cerrarse por el disconnect del host, fase quedó %v", r.phase)
		}
		if len(r.lobby) != 2 {
			t.Fatalf("el host desconectado no debería salir de r.lobby, quedaron %d", len(r.lobby))
		}
	})

	// El host vuelve (mismo camino de reconexión de siempre, con el token de
	// sesión que ya tenía — issueToken no vuelve a emitir uno nuevo mientras
	// no se pierda, ver docs/TOKENS.md) y la revancha sigue andando.
	newHostConn := r.Connect()
	r.HandleMessage(newHostConn, joinFrameWithToken("host", "Ana", hostToken))
	drainUntil(t, newHostConn, "room_state")

	r.HandleMessage(newHostConn, Frame{Type: "start_game"})
	drainUntil(t, newHostConn, "game_starting")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseActive {
			t.Fatalf("esperaba poder arrancar la revancha tras reconectar el host, fase quedó %v", r.phase)
		}
	})
}
