// Package room implementa el "nodo" de una partida: un goroutine dueño de
// su GameEngine, un canal de comandos entrantes y el fan-out de broadcast a
// las conexiones suscritas. Es un port de WsServer (Dart,
// network/websocket/websocket_server.dart) — la máquina de estados
// waiting→active→finished vive acá, no en un paquete lobby separado (ver la
// nota de diseño sobre por qué se movió).
package room

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

// Frame es un mensaje de red ya despojado de su transporte concreto:
// transport decodifica el Envelope de WebSocket y le pasa a room solo el
// discriminante y el payload crudo — room no importa transport (evita el
// ciclo transport -> room -> transport).
type Frame struct {
	Type string
	Raw  json.RawMessage
}

// Conn es el handle que transport usa para identificar una conexión ante el
// room. No es un PlayerID todavía: eso se resuelve recién cuando llega
// join_room (o cuando reconecta un jugador ya conocido).
type Conn struct {
	out chan Frame
}

func newConn() *Conn { return &Conn{out: make(chan Frame, 32)} }

// Out entrega los frames salientes destinados a esta conexión. transport
// los serializa y los escribe al socket real.
func (c *Conn) Out() <-chan Frame { return c.out }

type LobbyPlayer struct {
	ID      engine.PlayerID `json:"id"`
	Name    string          `json:"name"`
	IsHost  bool            `json:"isHost"`
	IsReady bool            `json:"isReady"`
}

type LobbyStatus string

const (
	LobbyWaiting  LobbyStatus = "waiting"
	LobbyStarting LobbyStatus = "starting"
)

type LobbySnapshot struct {
	ID         string          `json:"id"`
	HostID     engine.PlayerID `json:"hostId"`
	Players    []LobbyPlayer   `json:"players"`
	MaxPlayers int             `json:"maxPlayers"`
	Status     LobbyStatus     `json:"status"`
}

type Config struct {
	MaxPlayers    int
	GraceDuration time.Duration
}

type phase int

const (
	phaseWaiting phase = iota
	phaseActive
	phaseFinished
)

// Room es el nodo de una partida: vive desde "sala creada" hasta "partida
// terminada + grace period de reconexión expirado" (o hasta que la sala se
// vacía en fase de lobby). Todo su estado se toca únicamente dentro de
// run() — cmds es la única puerta de entrada, así que no hace falta mutex.
type Room struct {
	id       string
	gameType string
	hostID   engine.PlayerID
	cfg      Config

	cmds chan func(*Room)

	phase   phase
	lobby   []LobbyPlayer
	eng     engine.GameEngine
	state   engine.State
	clients map[engine.PlayerID]*Conn
	pending map[*Conn]bool
	tokens  map[engine.PlayerID]string
	timers  *timerSet

	done chan struct{}
}

func New(id, gameType string, host engine.PlayerInfo, cfg Config) *Room {
	return &Room{
		id:       id,
		gameType: gameType,
		hostID:   host.ID,
		cfg:      cfg,
		cmds:     make(chan func(*Room), 64),
		lobby:    []LobbyPlayer{{ID: host.ID, Name: host.Name, IsHost: true, IsReady: true}},
		clients:  make(map[engine.PlayerID]*Conn),
		pending:  make(map[*Conn]bool),
		tokens:   make(map[engine.PlayerID]string),
		timers:   newTimerSet(),
		done:     make(chan struct{}),
	}
}

// generateToken produce un secreto de sesión opaco (192 bits, base64
// URL-safe sin padding) — no es un JWT ni lleva claims, solo tiene que ser
// impredecible y único por sala; su alcance y vida útil son los de la sala
// misma (ver docs/TOKENS.md).
func generateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (r *Room) ID() string            { return r.id }
func (r *Room) GameType() string      { return r.gameType }
func (r *Room) Done() <-chan struct{} { return r.done }
func (r *Room) enqueue(fn func(*Room)) {
	select {
	case r.cmds <- fn:
	case <-r.done:
	}
}

// Run es el loop del goroutine dueño de la sala. Bloquea hasta que la
// partida termina y no quedan conexiones, o ctx se cancela.
func (r *Room) Run(ctx context.Context) {
	defer func() {
		r.timers.stopAll()
		close(r.done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-r.cmds:
			fn(r)
			if r.phase == phaseFinished && len(r.clients) == 0 {
				return
			}
		}
	}
}

// ── API pública: encolan un comando y devuelven de inmediato ───────────────

// Connect registra una conexión nueva "pendiente" (sin PlayerID asignado
// hasta que llegue join_room), como _pending en WsServer.
func (r *Room) Connect() *Conn {
	c := newConn()
	r.enqueue(func(r *Room) { r.pending[c] = true })
	return c
}

func (r *Room) HandleMessage(c *Conn, f Frame) {
	r.enqueue(func(r *Room) { r.dispatch(c, f) })
}

func (r *Room) HandleDisconnect(c *Conn) {
	r.enqueue(func(r *Room) { r.onDisconnect(c) })
}

// ── dispatch : equivalente a WsServer._dispatch ─────────────────────────────

func (r *Room) dispatch(c *Conn, f Frame) {
	switch f.Type {
	case "join_room":
		var m struct {
			PlayerID engine.PlayerID `json:"playerId"`
			Name     string          `json:"name"`
			Token    string          `json:"token,omitempty"`
		}
		if json.Unmarshal(f.Raw, &m) == nil {
			r.onJoin(c, m.PlayerID, m.Name, m.Token)
		}
	case "set_ready":
		var m struct {
			Ready bool `json:"ready"`
		}
		if json.Unmarshal(f.Raw, &m) == nil {
			r.onSetReady(c, m.Ready)
		}
	case "leave_room":
		var m struct {
			PlayerID engine.PlayerID `json:"playerId"`
		}
		if json.Unmarshal(f.Raw, &m) == nil {
			r.onLeave(m.PlayerID)
		}
	case "start_game":
		r.onStartGame(c)
	case "ping":
		r.sendTo(c, "pong", nil)
	case "action":
		var m struct {
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(f.Raw, &m) == nil {
			r.onAction(c, m.Payload)
		}
	case "player_reconnected":
		// El cliente Dart también manda esto explícitamente, aunque
		// _onJoin ya detecta la reconexión sola (jugador conocido que
		// vuelve a mandar join_room). Se trata igual, como confirmación
		// idempotente.
		if playerID, ok := r.playerFor(c); ok {
			r.onPlayerReconnected(playerID)
		}
	}
}

func (r *Room) onJoin(c *Conn, playerID engine.PlayerID, name string, token string) {
	delete(r.pending, c)

	for _, p := range r.lobby {
		if p.ID == playerID {
			// Jugador ya conocido (host, o reconectando). Si ya se le
			// había emitido un token, esta conexión tiene que probar que
			// es él antes de heredar su lugar en la sala — si no, ni
			// siquiera se registra en r.clients (ver docs/TOKENS.md).
			if existing, issued := r.tokens[playerID]; issued {
				if token == "" || token != existing {
					r.sendErrorTo(c, "Token de sesión inválido")
					return
				}
			} else if !r.issueToken(c, playerID) {
				return
			}

			r.clients[playerID] = c
			r.sendTo(c, "room_state", r.snapshot())
			r.onPlayerReconnected(playerID)
			return
		}
	}

	if r.phase != phaseWaiting {
		r.sendErrorTo(c, "La partida ya empezó")
		return
	}
	if len(r.lobby) >= r.cfg.MaxPlayers {
		r.sendErrorTo(c, "La sala está llena")
		return
	}

	if !r.issueToken(c, playerID) {
		return
	}
	r.clients[playerID] = c
	r.lobby = append(r.lobby, LobbyPlayer{ID: playerID, Name: name})
	r.broadcastRoomState()
}

// issueToken genera y guarda el token de sesión de playerID (primera
// conexión física que reclama esa identidad en la sala) y se lo manda solo
// a c, nunca por broadcast. Devuelve false si no se pudo generar — en ese
// caso ya le avisó el error a c y el caller no debe seguir el join.
func (r *Room) issueToken(c *Conn, playerID engine.PlayerID) bool {
	token, err := generateToken()
	if err != nil {
		r.sendErrorTo(c, "No se pudo iniciar la sesión")
		return false
	}
	r.tokens[playerID] = token
	r.sendTo(c, "session_token", map[string]string{"token": token})
	return true
}

func (r *Room) onSetReady(c *Conn, ready bool) {
	playerID, ok := r.playerFor(c)
	if !ok {
		return
	}
	for i, p := range r.lobby {
		if p.ID == playerID {
			r.lobby[i].IsReady = ready
		}
	}
	r.broadcastRoomState()
}

func (r *Room) onLeave(playerID engine.PlayerID) {
	delete(r.clients, playerID)
	delete(r.tokens, playerID)

	if playerID == r.hostID {
		r.broadcast("player_kicked", map[string]string{"reason": "El host cerró la sala"})
		r.closeAll()
		return
	}

	filtered := r.lobby[:0:0]
	for _, p := range r.lobby {
		if p.ID != playerID {
			filtered = append(filtered, p)
		}
	}
	r.lobby = filtered
	r.broadcastRoomState()
}

func (r *Room) onStartGame(c *Conn) {
	playerID, ok := r.playerFor(c)
	if !ok || playerID != r.hostID {
		r.sendErrorTo(c, "Solo el host puede empezar la partida")
		return
	}
	if r.phase != phaseWaiting {
		return
	}
	if !r.canStart() {
		r.sendErrorTo(c, "Faltan jugadores listos")
		return
	}

	factory, ok := engine.Lookup(r.gameType)
	if !ok {
		r.sendErrorTo(c, fmt.Sprintf("Tipo de juego desconocido: %s", r.gameType))
		return
	}
	eng, err := factory(nil)
	if err != nil {
		r.sendErrorTo(c, err.Error())
		return
	}

	players := make([]engine.PlayerInfo, len(r.lobby))
	for i, p := range r.lobby {
		players[i] = engine.PlayerInfo{ID: p.ID, Name: p.Name}
	}
	state, err := eng.Start(players, nil)
	if err != nil {
		r.sendErrorTo(c, err.Error())
		return
	}

	r.eng = eng
	r.state = state
	r.phase = phaseActive
	r.broadcast("game_starting", nil)
	r.broadcastState()
}

func (r *Room) canStart() bool {
	if len(r.lobby) < 2 {
		return false
	}
	for _, p := range r.lobby {
		if !p.IsHost && !p.IsReady {
			return false
		}
	}
	return true
}

func (r *Room) onAction(c *Conn, raw json.RawMessage) {
	playerID, ok := r.playerFor(c)
	if !ok || r.phase != phaseActive {
		return
	}

	action, err := r.eng.DecodeAction(playerID, raw)
	if err != nil {
		r.sendTo(c, "action_rejected", map[string]string{"message": err.Error()})
		return
	}
	next, events, err := r.eng.Apply(r.state, action)
	if err != nil {
		r.sendTo(c, "action_rejected", map[string]string{"message": err.Error()})
		return
	}

	r.state = next
	r.dispatchEvents(events)
	r.broadcastState()
	r.maybeFinish()
	r.syncReactionTimer()
}

func (r *Room) onDisconnect(c *Conn) {
	delete(r.pending, c)
	playerID, ok := r.playerFor(c)
	if !ok {
		return
	}

	if r.phase == phaseActive {
		delete(r.clients, playerID)
		r.timers.trackDisconnect(playerID, r.cfg.GraceDuration, func() {
			r.enqueue(func(r *Room) { r.onGraceExpired(playerID) })
		})
		if r.eng != nil {
			if next, events, err := r.eng.MarkPlayerDisconnected(r.state, playerID); err == nil {
				r.state = next
				r.dispatchEvents(events)
				r.broadcastState()
			}
		}
		return
	}
	r.onLeave(playerID)
}

func (r *Room) onPlayerReconnected(playerID engine.PlayerID) {
	r.timers.cancelDisconnect(playerID)
	if r.eng == nil || r.phase != phaseActive {
		return
	}
	if next, events, err := r.eng.MarkPlayerReconnected(r.state, playerID); err == nil {
		r.state = next
		r.dispatchEvents(events)
		r.broadcastState()
	}
}

// onGraceExpired: expiró el grace period sin que el jugador volviera —
// dispara EliminateForDisconnect, igual que ReconnectionManager al agotar
// su Timer en el original.
func (r *Room) onGraceExpired(playerID engine.PlayerID) {
	if r.eng == nil {
		return
	}
	next, events, err := r.eng.EliminateForDisconnect(r.state, playerID)
	if err != nil {
		return
	}
	r.state = next
	r.dispatchEvents(events)
	r.broadcastState()
	r.maybeFinish()
}

// syncReactionTimer le pregunta al motor si el estado actual necesita un
// timer de seguimiento (PendingTimer) y lo reprograma. El room no sabe qué
// es una "ventana de Nope" — solo obedece.
func (r *Room) syncReactionTimer() {
	if r.eng == nil {
		return
	}
	d, ok := r.eng.PendingTimer(r.state)
	r.timers.setReaction(d, ok, func() {
		r.enqueue(func(r *Room) { r.onReactionTimeout() })
	})
}

func (r *Room) onReactionTimeout() {
	if r.eng == nil {
		return
	}
	next, events, err := r.eng.ResolveNopeWindow(r.state)
	if err != nil {
		return
	}
	r.state = next
	r.dispatchEvents(events)
	r.broadcastState()
	r.syncReactionTimer() // por si la resolución encadena otra ventana
}

func (r *Room) maybeFinish() {
	if term, ok := r.state.(engine.Terminal); ok && term.Terminal() {
		r.phase = phaseFinished
	}
}

// ── helpers de envío ─────────────────────────────────────────────────────

func (r *Room) playerFor(c *Conn) (engine.PlayerID, bool) {
	for id, conn := range r.clients {
		if conn == c {
			return id, true
		}
	}
	return "", false
}

func (r *Room) sendTo(c *Conn, msgType string, payload any) {
	raw, _ := json.Marshal(payload)
	r.sendToRaw(c, msgType, raw)
}

func (r *Room) sendToRaw(c *Conn, msgType string, raw json.RawMessage) {
	select {
	case c.out <- Frame{Type: msgType, Raw: raw}:
	default:
		// Conexión lenta o ya caída: no bloquear el goroutine de la sala
		// por un solo cliente atascado.
	}
}

func (r *Room) sendErrorTo(c *Conn, message string) {
	r.sendTo(c, "ws_error", map[string]string{"message": message})
}

func (r *Room) broadcast(msgType string, payload any) {
	raw, _ := json.Marshal(payload)
	r.broadcastRaw(msgType, raw)
}

func (r *Room) broadcastRaw(msgType string, raw json.RawMessage) {
	for _, c := range r.clients {
		r.sendToRaw(c, msgType, raw)
	}
}

func (r *Room) broadcastRoomState() {
	r.broadcast("room_state", r.snapshot())
}

func (r *Room) snapshot() LobbySnapshot {
	status := LobbyWaiting
	if r.phase != phaseWaiting {
		status = LobbyStarting
	}
	return LobbySnapshot{
		ID:         r.id,
		HostID:     r.hostID,
		Players:    append([]LobbyPlayer{}, r.lobby...),
		MaxPlayers: r.cfg.MaxPlayers,
		Status:     status,
	}
}

// broadcastState manda a cada jugador SU PROPIA proyección del estado
// (GameEngine.View) — nunca el mismo payload a todos. Es la pieza que
// WsServer no tiene: ahí alcanzaba un solo broadcast() porque LAN confía en
// que el cliente no muestre lo que no debe.
func (r *Room) broadcastState() {
	for playerID, c := range r.clients {
		view, err := r.eng.View(r.state, playerID)
		if err != nil {
			continue
		}
		r.sendTo(c, "game_state", view)
	}
}

func (r *Room) dispatchEvents(events []engine.Event) {
	for _, ev := range events {
		inner := flattenEvent(ev)
		if len(ev.Recipients) == 0 {
			r.broadcastRaw("game_event", inner)
			continue
		}
		for _, pid := range ev.Recipients {
			if c, ok := r.clients[pid]; ok {
				r.sendToRaw(c, "game_event", inner)
			}
		}
	}
}

// flattenEvent arma el objeto GameEvent tal como lo espera GameEvent.fromJson
// en Dart: type/timestamp junto a los campos propios del evento en un único
// nivel (no {"type":..,"payload":{...}} — ese anidado extra es el de
// GameEventMessage por fuera, que agrega transport al envolver el frame).
func flattenEvent(ev engine.Event) json.RawMessage {
	m := map[string]json.RawMessage{}
	if raw, err := json.Marshal(ev.Payload); err == nil {
		_ = json.Unmarshal(raw, &m) // si Payload no serializa a un objeto (p. ej. nil), m queda vacío
	}
	m["type"], _ = json.Marshal(ev.Type)
	m["timestamp"], _ = json.Marshal(ev.Timestamp)
	out, _ := json.Marshal(m)
	return out
}

func (r *Room) closeAll() {
	for _, c := range r.clients {
		close(c.out)
	}
	r.clients = make(map[engine.PlayerID]*Conn)
	r.phase = phaseFinished
}
