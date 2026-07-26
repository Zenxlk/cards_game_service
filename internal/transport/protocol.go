// Package transport es el servidor WebSocket y la codificación de red — la
// única pieza acoplada al formato exacto que ya habla el cliente Flutter
// (lib/network/websocket/websocket_message.dart). room y pkg/engine no
// conocen este formato; si mañana se agrega otro tipo de cliente con otro
// formato de wire, es acá donde se adapta, sin tocar reglas de juego.
package transport

import (
	"encoding/json"
	"fmt"

	"github.com/ZenXLK/cards_game_service/internal/room"
)

// Discriminantes de WsMessage.type — deben calzar carácter por carácter con
// el switch de WsMessage.fromJson/toJson en el cliente.
const (
	TypeJoinRoom          = "join_room"
	TypeSetReady          = "set_ready"
	TypeLeaveRoom         = "leave_room"
	TypeStartGame         = "start_game"
	TypeRoomState         = "room_state"
	TypeGameStarting      = "game_starting"
	TypePlayerKicked      = "player_kicked"
	TypeWsError           = "ws_error"
	TypePing              = "ping"
	TypePong              = "pong"
	TypeGameState         = "game_state"
	TypeAction            = "action"
	TypePlayerReconnected = "player_reconnected"
	TypeGameEvent         = "game_event"
	TypeActionRejected    = "action_rejected"
	TypeSessionToken      = "session_token"
)

// wireShape describe cómo se combinan el "type" y el resto de los campos de
// un frame saliente — verificado leyendo el toJson() real de cada WsMessage
// concreto, no inferido: no es un envelope uniforme.
type wireShape int

const (
	// shapeFlat: los campos del payload van sueltos junto a "type"
	// (ws_error: {type, message}; player_kicked: {type, reason}).
	shapeFlat wireShape = iota
	// shapeNamed: el payload entero se anida bajo una clave fija
	// (game_state/game_event: "payload"; room_state: "room").
	shapeNamed
	// shapeEmpty: el mensaje no lleva más campos que "type"
	// (game_starting, pong).
	shapeEmpty
)

type shapeSpec struct {
	shape wireShape
	key   string // solo usado si shape == shapeNamed
}

var outboundShapes = map[string]shapeSpec{
	TypeRoomState:      {shapeNamed, "room"},
	TypeGameStarting:   {shapeEmpty, ""},
	TypePlayerKicked:   {shapeFlat, ""},
	TypeWsError:        {shapeFlat, ""},
	TypeGameState:      {shapeNamed, "payload"},
	TypeActionRejected: {shapeFlat, ""},
	TypeGameEvent:      {shapeNamed, "payload"},
	TypePong:           {shapeEmpty, ""},
}

// encodeFrame arma los bytes finales de un room.Frame saliente respetando
// el formato del WsMessage concreto que representa.
func encodeFrame(f room.Frame) ([]byte, error) {
	spec, ok := outboundShapes[f.Type]
	if !ok {
		spec = shapeSpec{shapeFlat, ""}
	}

	switch spec.shape {
	case shapeEmpty:
		return json.Marshal(map[string]string{"type": f.Type})

	case shapeNamed:
		payload := f.Raw
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		m := map[string]json.RawMessage{}
		typeRaw, err := json.Marshal(f.Type)
		if err != nil {
			return nil, err
		}
		m["type"] = typeRaw
		m[spec.key] = payload
		return json.Marshal(m)

	default: // shapeFlat
		m := map[string]json.RawMessage{}
		if len(f.Raw) > 0 {
			if err := json.Unmarshal(f.Raw, &m); err != nil {
				return nil, fmt.Errorf("transport: no se pudo aplanar el payload de %q: %w", f.Type, err)
			}
		}
		typeRaw, err := json.Marshal(f.Type)
		if err != nil {
			return nil, err
		}
		m["type"] = typeRaw
		return json.Marshal(m)
	}
}

// decodeFrame lee el discriminante "type" de un frame entrante y le pasa a
// room los bytes crudos completos — room ya sabe, por tipo, si tiene que
// leer campos sueltos o bajo "payload" (ver room.dispatch).
func decodeFrame(raw []byte) (room.Frame, error) {
	var e struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return room.Frame{}, fmt.Errorf("transport: frame inválido: %w", err)
	}
	return room.Frame{Type: e.Type, Raw: raw}, nil
}
