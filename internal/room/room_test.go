package room

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"

	"github.com/ZenXLK/cards_game_service/internal/auth"
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

// terminableState/terminableEngine es un motor mínimo controlable desde el
// test: la primera "action" que llega termina la partida y declara ganador
// a quien la mandó. Sirve para probar finalizeMatch sin tener que jugar una
// partida real de Exploding Kittens.
type terminableState struct {
	terminal  bool
	winnerID  engine.PlayerID
	hasWinner bool
}

func (s terminableState) Terminal() bool                  { return s.terminal }
func (s terminableState) Winner() (engine.PlayerID, bool) { return s.winnerID, s.hasWinner }

type terminableEngine struct{}

func (terminableEngine) Start(players []engine.PlayerInfo, _ json.RawMessage) (engine.State, error) {
	return terminableState{}, nil
}
func (terminableEngine) DecodeAction(actor engine.PlayerID, _ json.RawMessage) (engine.Action, error) {
	return actor, nil
}
func (terminableEngine) Apply(_ engine.State, action engine.Action) (engine.State, []engine.Event, error) {
	winner := action.(engine.PlayerID)
	return terminableState{terminal: true, winnerID: winner, hasWinner: true}, nil, nil
}
func (terminableEngine) ResolveNopeWindow(s engine.State) (engine.State, []engine.Event, error) {
	return s, nil, nil
}
func (terminableEngine) MarkPlayerDisconnected(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}
func (terminableEngine) MarkPlayerReconnected(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}
func (terminableEngine) EliminateForDisconnect(s engine.State, _ engine.PlayerID) (engine.State, []engine.Event, error) {
	return s, nil, nil
}
func (terminableEngine) View(s engine.State, _ engine.PlayerID) (any, error) { return s, nil }
func (terminableEngine) PendingTimer(engine.State) (time.Duration, bool)     { return 0, false }

func init() {
	engine.Register("room_test_terminable", func(json.RawMessage) (engine.GameEngine, error) {
		return terminableEngine{}, nil
	})
}

// syncRoom encola fn en el goroutine de la sala y espera a que corra, para
// poder leer campos internos de Room desde el test sin pisar al goroutine
// dueño del estado (evita datarace bajo -race).
func syncRoom(t *testing.T, r *Room, fn func(*Room)) {
	t.Helper()
	done := make(chan struct{})
	r.enqueue(func(r *Room) {
		fn(r)
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout esperando sincronizar con el goroutine de la sala")
	}
}

func joinFrame(playerID, name string) Frame {
	raw, _ := json.Marshal(map[string]string{"playerId": playerID, "name": name})
	return Frame{Type: "join_room", Raw: raw}
}

func joinFrameWithAuth(playerID, name, authToken string) Frame {
	raw, _ := json.Marshal(map[string]string{"playerId": playerID, "name": name, "authToken": authToken})
	return Frame{Type: "join_room", Raw: raw}
}

// testAuthFixture levanta un servidor JWKS real (httptest) respaldado por un
// único secreto HMAC, y expone auth.NewVerifier apuntando a él — así se
// prueba el mismo camino de red que usa producción, sin pegarle a Supabase.
type testAuthFixture struct {
	verifier *auth.Verifier
	secret   []byte
	kid      string
}

func newTestAuthFixture(t *testing.T) *testAuthFixture {
	t.Helper()
	ctx := context.Background()
	secret := []byte("test-hmac-secret")
	kid := "test-kid"

	jwk, err := jwkset.NewJWKFromKey(secret, jwkset.JWKOptions{
		Marshal: jwkset.JWKMarshalOptions{Private: true},
		Metadata: jwkset.JWKMetadataOptions{
			ALG: jwkset.AlgHS256,
			KID: kid,
			USE: jwkset.UseSig,
		},
	})
	if err != nil {
		t.Fatalf("jwkset.NewJWKFromKey: %v", err)
	}
	memStore := jwkset.NewMemoryStorage()
	if err := memStore.KeyWrite(ctx, jwk); err != nil {
		t.Fatalf("KeyWrite: %v", err)
	}
	jwksJSON, err := memStore.JSON(ctx)
	if err != nil {
		t.Fatalf("memStore.JSON: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	}))
	t.Cleanup(srv.Close)

	verifier, err := auth.NewVerifier(ctx, srv.URL)
	if err != nil {
		t.Fatalf("auth.NewVerifier: %v", err)
	}
	return &testAuthFixture{verifier: verifier, secret: secret, kid: kid}
}

func (f *testAuthFixture) sign(t *testing.T, sub string, isAnonymous bool) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":          sub,
		"is_anonymous": isAnonymous,
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	token.Header[jwkset.HeaderKID] = f.kid
	signed, err := token.SignedString(f.secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
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

// TestCloseAll_SendsErrorAndClosesChannelForPendingConn cubre el gap
// identificado en el hardening: una conexión que abrió el WebSocket pero
// nunca completó join_room (r.pending) tiene que recibir una señal cuando
// la sala se cierra — antes de este fix, closeAll() solo cerraba
// r.clients y una conexión pending se quedaba esperando una respuesta que
// nunca llegaba.
func TestCloseAll_SendsErrorAndClosesChannelForPendingConn(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM11", "room_test_panicky", host, Config{MaxPlayers: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	pendingConn := r.Connect() // nunca manda join_room

	syncRoom(t, r, func(r *Room) {
		if !r.pending[pendingConn] {
			t.Fatal("esperaba que pendingConn estuviera en r.pending antes del cierre")
		}
	})

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrame("p1", "Ana"))
	drainUntil(t, hostConn, "room_state")
	// onLeave saca a hostConn de r.clients ANTES de emitir player_kicked
	// (así que el propio host que se va nunca lo recibe) y dispara
	// closeAll() porque playerID == hostID — no hace falta esperar nada en
	// hostConn, alcanza con confirmar la señal en pendingConn.
	r.HandleMessage(hostConn, Frame{Type: "leave_room", Raw: json.RawMessage(`{"playerId":"p1"}`)})

	f := drainUntil(t, pendingConn, "ws_error")
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(f.Raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message == "" {
		t.Fatal("esperaba un mensaje de error para la conexión pending")
	}

	select {
	case _, ok := <-pendingConn.Out():
		if ok {
			t.Fatal("esperaba que el canal de pendingConn se cerrara tras el ws_error")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout esperando que se cerrara el canal de pendingConn")
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

func TestOnJoin_ValidAuthTokenOverridesPlayerID(t *testing.T) {
	authFixture := newTestAuthFixture(t)
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM5", "room_test_panicky", host, Config{MaxPlayers: 2, Auth: authFixture.verifier})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrame("p1", "Ana"))
	drainUntil(t, hostConn, "room_state")

	const realSub = "5f1c9c1e-2b3a-4a3e-9c1e-2b3a4a3e9c1e"
	authTok := authFixture.sign(t, realSub, false)

	guestConn := r.Connect()
	r.HandleMessage(guestConn, joinFrameWithAuth("lo-que-mande-el-cliente-aca-se-ignora", "Beto", authTok))
	f := drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	var snapshot LobbySnapshot
	if err := json.Unmarshal(f.Raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range snapshot.Players {
		if p.ID == engine.PlayerID(realSub) {
			found = true
		}
		if string(p.ID) == "lo-que-mande-el-cliente-aca-se-ignora" {
			t.Fatal("el playerId mandado por el cliente no debería sobrevivir con un authToken válido")
		}
	}
	if !found {
		t.Fatalf("esperaba encontrar %q entre los jugadores, snapshot=%+v", realSub, snapshot.Players)
	}
}

func TestOnJoin_InvalidAuthTokenRejectsJoin(t *testing.T) {
	authFixture := newTestAuthFixture(t)
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM6", "room_test_panicky", host, Config{MaxPlayers: 2, Auth: authFixture.verifier})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	c := r.Connect()
	r.HandleMessage(c, joinFrameWithAuth("p2", "Beto", "esto-no-es-un-jwt-valido"))

	f := drainUntil(t, c, "ws_error")
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(f.Raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != "Token de autenticación inválido" {
		t.Fatalf("mensaje inesperado: %q", payload.Message)
	}
}

func TestOnJoin_TracksAuthenticatedPlayersSeparatelyFromGuests(t *testing.T) {
	authFixture := newTestAuthFixture(t)
	// Un cliente autenticado manda su propio uuid de Supabase como hostId
	// desde POST /rooms, antes incluso de abrir el WebSocket — authToken en
	// join_room solo lo verifica, no sustituye ese contrato.
	const hostSub = "11111111-1111-1111-1111-111111111111"
	host := engine.PlayerInfo{ID: engine.PlayerID(hostSub), Name: "Ana"}
	r := New("ROOM7", "room_test_panicky", host, Config{MaxPlayers: 2, Auth: authFixture.verifier})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrameWithAuth(hostSub, "Ana", authFixture.sign(t, hostSub, false)))
	drainUntil(t, hostConn, "room_state")

	guestConn := r.Connect()
	r.HandleMessage(guestConn, joinFrame("p2", "Beto"))
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	syncRoom(t, r, func(r *Room) {
		if !r.authPlayers[engine.PlayerID(hostSub)] {
			t.Errorf("esperaba que el host autenticado (%s) apareciera en authPlayers", hostSub)
		}
		if r.authPlayers["p2"] {
			t.Error("el invitado sin JWT no debería aparecer en authPlayers")
		}
	})
}

func TestFinalizeMatch_RecordsWinnerOnNormalTermination(t *testing.T) {
	authFixture := newTestAuthFixture(t)
	const hostSub = "22222222-2222-2222-2222-222222222222"
	host := engine.PlayerInfo{ID: engine.PlayerID(hostSub), Name: "Ana"}
	r := New("ROOM8", "room_test_terminable", host, Config{MaxPlayers: 2, Auth: authFixture.verifier})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	hostConn := r.Connect()
	r.HandleMessage(hostConn, joinFrameWithAuth(hostSub, "Ana", authFixture.sign(t, hostSub, false)))
	drainUntil(t, hostConn, "room_state")

	guestConn := r.Connect()
	r.HandleMessage(guestConn, joinFrame("p2", "Beto")) // invitado, sin JWT
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(guestConn, Frame{Type: "set_ready", Raw: json.RawMessage(`{"ready":true}`)})
	drainUntil(t, guestConn, "room_state")
	drainUntil(t, hostConn, "room_state")

	r.HandleMessage(hostConn, Frame{Type: "start_game"})
	drainUntil(t, hostConn, "game_starting")

	syncRoom(t, r, func(r *Room) {
		if r.matchID == "" {
			t.Error("esperaba un matchID asignado tras start_game")
		}
	})

	// terminableEngine: cualquier "action" termina la partida y declara
	// ganador a quien la mandó — acá, el host autenticado.
	r.HandleMessage(hostConn, Frame{Type: "action", Raw: json.RawMessage(`{"payload":{}}`)})
	drainUntil(t, hostConn, "game_state")

	syncRoom(t, r, func(r *Room) {
		if r.phase != phaseFinished {
			t.Fatalf("esperaba phaseFinished, quedó %v", r.phase)
		}
		if !r.matchFinalized {
			t.Fatal("esperaba matchFinalized=true tras terminar la partida")
		}
		w, ok := r.state.(engine.Winner)
		if !ok {
			t.Fatal("terminableState debería implementar engine.Winner")
		}
		winnerID, hasWinner := w.Winner()
		if !hasWinner || winnerID != engine.PlayerID(hostSub) {
			t.Fatalf("ganador inesperado: %v (hasWinner=%v)", winnerID, hasWinner)
		}
	})
}

func TestFinalizeMatch_SetsFlagOnCrashDuringActiveMatch(t *testing.T) {
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	r := New("ROOM9", "room_test_panicky", host, Config{MaxPlayers: 2})

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

	// panickyEngine.Apply panica a propósito — safeExec lo recupera y
	// dispara crashClose, que debe marcar la partida como abandonada.
	r.HandleMessage(hostConn, Frame{Type: "action", Raw: json.RawMessage(`{"payload":{}}`)})
	drainUntil(t, hostConn, "player_kicked")

	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("la sala no cerró su goroutine tras el panic recuperado")
	}

	// El goroutine de la sala ya terminó (r.Done() cerrado): leer el campo
	// acá es seguro, nadie más lo toca.
	if r.matchID == "" {
		t.Fatal("esperaba un matchID asignado tras start_game")
	}
	if !r.matchFinalized {
		t.Fatal("esperaba matchFinalized=true tras el cierre por panic (abandoned)")
	}
}
