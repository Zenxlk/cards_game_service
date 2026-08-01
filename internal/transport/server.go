package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/ZenXLK/cards_game_service/internal/auth"
	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/internal/store"
	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

// rateLimitPerSecond y rateLimitBurst rigen el límite por IP aplicado a
// todas las rutas (ver rateLimitMiddleware) — generoso para un cliente
// legítimo (crear una sala, reconectar unas veces seguidas), restrictivo
// contra una ráfaga automatizada. No configurable por env var a propósito:
// el techo real contra un ataque sostenido es lobby.Config.MaxRooms, esto
// es la primera línea de contención, no necesita ajuste por instancia.
const (
	rateLimitPerSecond = 5
	rateLimitBurst     = 10
)

type Config struct {
	ReadLimit int64

	// Store persiste eventos de auditoría de rechazos en POST /rooms (antes
	// de que exista una sala, y por ende un room.Room con su propio
	// cfg.Store) y sirve las lecturas de perfil/leaderboard. nil significa
	// "sin persistencia configurada": el server funciona igual, esos
	// endpoints devuelven 503 y el audit log de POST /rooms queda deshabilitado.
	Store *store.Store

	// Auth valida el JWT de Supabase en PATCH /players/{id}/nickname (el
	// único endpoint HTTP, aparte de join_room, que necesita saber quién es
	// el que llama). nil significa "sin Supabase configurado": el endpoint
	// rechaza todo con 401.
	Auth *auth.Verifier
}

func DefaultConfig() Config {
	return Config{ReadLimit: 1 << 16} // 64 KiB: de sobra para cualquier action/state de una partida de cartas
}

// Server expone /ws/{code} para el juego y un /rooms mínimo para crear
// salas. lobby/room no importan este paquete — Server es la única pieza
// acoplada a HTTP y al formato de wire concreto (protocol.go).
type Server struct {
	// ctx es el contexto de vida del PROCESO, no de un request HTTP en
	// particular: las salas que arrancan acá deben sobrevivir a la request
	// que las creó. Nunca pasar r.Context() a lobby.CreateRoom.
	ctx     context.Context
	lobby   *lobby.Lobby
	cfg     Config
	limiter *ipRateLimiter
}

func NewServer(ctx context.Context, l *lobby.Lobby, cfg Config) *Server {
	return &Server{ctx: ctx, lobby: l, cfg: cfg, limiter: newIPRateLimiter(rateLimitPerSecond, rateLimitBurst)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /rooms", s.handleCreateRoom)
	mux.HandleFunc("GET /ws/", s.handleWS)
	mux.HandleFunc("GET /players/{id}", s.handleGetPlayer)
	mux.HandleFunc("PATCH /players/{id}/nickname", s.handleUpdateNickname)
	mux.HandleFunc("GET /leaderboard", s.handleLeaderboard)
	return rateLimitMiddleware(s.limiter, mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

type createRoomRequest struct {
	GameType string `json:"gameType"`
	HostID   string `json:"hostId"`
	HostName string `json:"hostName"`
}

type createRoomResponse struct {
	Code string `json:"code"`
}

// maxCreateRoomBody acota el body de POST /rooms — el payload esperado son
// tres strings cortos; sin este límite, json.Decoder acepta un body de
// cualquier tamaño (a diferencia del WebSocket, que ya tiene ReadLimit).
const maxCreateRoomBody = 4 << 10 // 4 KiB

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateRoomBody)

	var req createRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido o demasiado grande", http.StatusBadRequest)
		s.cfg.Store.RecordAuditEvent("", nil, "create_room_rejected_invalid_json", nil)
		return
	}
	if req.GameType == "" || req.HostID == "" {
		http.Error(w, "gameType y hostId son obligatorios", http.StatusBadRequest)
		return
	}
	if len(req.HostID) > room.MaxPlayerIDLen || len(req.HostName) > room.MaxDisplayNameLen {
		http.Error(w, "hostId o hostName demasiado largos", http.StatusBadRequest)
		s.cfg.Store.RecordAuditEvent("", nil, "create_room_rejected_oversized_fields", nil)
		return
	}

	host := engine.PlayerInfo{ID: engine.PlayerID(req.HostID), Name: req.HostName}
	_, code, err := s.lobby.CreateRoom(s.ctx, req.GameType, host)
	if errors.Is(err, lobby.ErrTooManyRooms) {
		http.Error(w, "el servidor está al límite de salas activas, probá de nuevo en un rato", http.StatusServiceUnavailable)
		s.cfg.Store.RecordAuditEvent("", nil, "create_room_rejected_max_rooms", nil)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(createRoomResponse{Code: code})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/ws/")
	if code == "" {
		http.Error(w, "falta el código de sala", http.StatusBadRequest)
		return
	}
	rm, ok := s.lobby.Get(code)
	if !ok {
		http.Error(w, "sala no encontrada", http.StatusNotFound)
		return
	}

	wsConn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	wsConn.SetReadLimit(s.cfg.ReadLimit)

	rc := rm.Connect()
	serveConn(r.Context(), wsConn, rm, rc)
}

// serveConn corre mientras dure la conexión: readPump y writePump en
// paralelo, atados al mismo contexto — cuando uno termina (el cliente cierra
// el socket, hay un error de red, o el propio contexto del request se
// cancela), cancel() para al otro. HandleDisconnect siempre se llama al
// final, dispare lo que dispare la salida.
func serveConn(parent context.Context, ws *websocket.Conn, rm *room.Room, rc *room.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()
	defer rm.HandleDisconnect(rc)

	go writePump(ctx, cancel, ws, rc)
	readPump(ctx, cancel, ws, rm, rc)
}

func readPump(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn, rm *room.Room, rc *room.Conn) {
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		frame, err := decodeFrame(data)
		if err != nil {
			continue // frame malformado: se ignora, no se tira la conexión entera
		}
		rm.HandleMessage(rc, frame)
	}
}

func writePump(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn, rc *room.Conn) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-rc.Out():
			if !ok {
				return
			}
			data, err := encodeFrame(f)
			if err != nil {
				continue
			}
			wctx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
			err = ws.Write(wctx, websocket.MessageText, data)
			cancelWrite()
			if err != nil {
				return
			}
		}
	}
}

// maxProfileBody acota el body de PATCH /players/{id}/nickname — un único
// string corto, mismo criterio que maxCreateRoomBody.
const maxProfileBody = 4 << 10 // 4 KiB

func (s *Server) handleGetPlayer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// players.id es uuid en Postgres — validar acá evita un error de sintaxis
	// SQL genérico (500) para lo que en realidad es una request malformada (400).
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}

	profile, err := s.cfg.Store.GetPlayerProfile(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotConfigured):
		http.Error(w, "persistencia no configurada", http.StatusServiceUnavailable)
		return
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "jugador no encontrado", http.StatusNotFound)
		return
	case err != nil:
		slog.Error("handleGetPlayer: GetPlayerProfile falló", "err", err, "playerId", id)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profile)
}

type updateNicknameRequest struct {
	Nickname string `json:"nickname"`
}

// handleUpdateNickname exige el propio JWT del jugador — nadie puede
// cambiar el nickname de otro id, ni siquiera un invitado que adivine el
// uuid: bearerToken tiene que validar contra Supabase y su "sub" tiene que
// matchear exactamente el {id} de la ruta.
func (s *Server) handleUpdateNickname(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	identity, err := s.cfg.Auth.Verify(bearerToken(r))
	if err != nil || identity.PlayerID != id {
		http.Error(w, "no autorizado", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxProfileBody)
	var req updateNicknameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido o demasiado grande", http.StatusBadRequest)
		return
	}
	if len(req.Nickname) == 0 || len(req.Nickname) > room.MaxDisplayNameLen {
		http.Error(w, "nickname inválido", http.StatusBadRequest)
		return
	}

	if err := s.cfg.Store.UpdateNickname(r.Context(), id, req.Nickname); err != nil {
		if errors.Is(err, store.ErrNotConfigured) {
			http.Error(w, "persistencia no configurada", http.StatusServiceUnavailable)
			return
		}
		slog.Error("handleUpdateNickname: UpdateNickname falló", "err", err, "playerId", id)
		http.Error(w, "no se pudo actualizar el nickname", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

const (
	defaultLeaderboardLimit = 20
	maxLeaderboardLimit     = 100
)

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	limit := defaultLeaderboardLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= maxLeaderboardLimit {
			limit = n
		}
	}

	entries, err := s.cfg.Store.GetLeaderboard(r.Context(), limit)
	if errors.Is(err, store.ErrNotConfigured) {
		http.Error(w, "persistencia no configurada", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		slog.Error("handleLeaderboard: GetLeaderboard falló", "err", err, "limit", limit)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}
