package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

type Config struct {
	ReadLimit int64
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
	ctx   context.Context
	lobby *lobby.Lobby
	cfg   Config
}

func NewServer(ctx context.Context, l *lobby.Lobby, cfg Config) *Server {
	return &Server{ctx: ctx, lobby: l, cfg: cfg}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /rooms", s.handleCreateRoom)
	mux.HandleFunc("GET /ws/", s.handleWS)
	return mux
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

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var req createRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	if req.GameType == "" || req.HostID == "" {
		http.Error(w, "gameType y hostId son obligatorios", http.StatusBadRequest)
		return
	}

	host := engine.PlayerInfo{ID: engine.PlayerID(req.HostID), Name: req.HostName}
	_, code, err := s.lobby.CreateRoom(s.ctx, req.GameType, host)
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
