// Package lobby es el registro central de salas: crear, listar, unirse por
// código. A diferencia de WsServer (Dart) — donde un proceso siempre corre
// una sola sala porque el host la aloja en su propio teléfono — acá muchas
// salas viven concurrentemente en el mismo proceso, así que lobby es la
// pieza genuinamente nueva que no existe del lado LAN. La lógica de
// join/ready/leave/start vive en room.Room, no acá.
package lobby

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // sin I/O/0/1: ambiguos al leerlos en voz alta

type Config struct {
	CodeLength int
	Room       room.Config
}

type Lobby struct {
	cfg Config

	mu    sync.RWMutex
	rooms map[string]*room.Room
}

func New(cfg Config) *Lobby {
	return &Lobby{cfg: cfg, rooms: make(map[string]*room.Room)}
}

// CreateRoom provisiona una sala nueva para gameType, genera un código de
// unión y arranca su goroutine (room.Room.Run). El caller es responsable de
// pasar un context que viva al menos tanto como el proceso del servidor.
func (l *Lobby) CreateRoom(ctx context.Context, gameType string, host engine.PlayerInfo) (*room.Room, string, error) {
	if _, ok := engine.Lookup(gameType); !ok {
		return nil, "", fmt.Errorf("lobby: tipo de juego desconocido: %s", gameType)
	}

	code, err := l.uniqueCode()
	if err != nil {
		return nil, "", err
	}

	r := room.New(code, gameType, host, l.cfg.Room)

	l.mu.Lock()
	l.rooms[code] = r
	l.mu.Unlock()

	go func() {
		r.Run(ctx)
		l.mu.Lock()
		delete(l.rooms, code)
		l.mu.Unlock()
	}()

	return r, code, nil
}

func (l *Lobby) Get(code string) (*room.Room, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	r, ok := l.rooms[code]
	return r, ok
}

type Summary struct {
	Code     string `json:"code"`
	GameType string `json:"gameType"`
}

func (l *Lobby) List() []Summary {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Summary, 0, len(l.rooms))
	for code, r := range l.rooms {
		out = append(out, Summary{Code: code, GameType: r.GameType()})
	}
	return out
}

func (l *Lobby) uniqueCode() (string, error) {
	for range 20 {
		code, err := randomCode(l.cfg.CodeLength)
		if err != nil {
			return "", err
		}
		l.mu.RLock()
		_, taken := l.rooms[code]
		l.mu.RUnlock()
		if !taken {
			return code, nil
		}
	}
	return "", fmt.Errorf("lobby: no se pudo generar un código de sala único")
}

func randomCode(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out), nil
}
