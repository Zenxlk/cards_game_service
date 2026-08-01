package lobby

import (
	"context"
	"testing"
	"time"

	_ "github.com/ZenXLK/cards_game_service/games/fixture"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/pkg/engine"
)

func TestCreateRoom_RejectsOverMaxRooms(t *testing.T) {
	l := New(Config{CodeLength: 4, MaxRooms: 1, Room: room.Config{MaxPlayers: 2}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}
	if _, _, err := l.CreateRoom(ctx, "fixture", host); err != nil {
		t.Fatalf("primera sala: no esperaba error, dio %v", err)
	}

	if _, _, err := l.CreateRoom(ctx, "fixture", host); err != ErrTooManyRooms {
		t.Fatalf("segunda sala: esperaba ErrTooManyRooms, dio %v", err)
	}
}

func TestCreateRoom_FreesCapacityWhenARoomCloses(t *testing.T) {
	l := New(Config{CodeLength: 4, MaxRooms: 1, Room: room.Config{MaxPlayers: 2}})
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}

	roomCtx, cancelRoom := context.WithCancel(context.Background())
	r, _, err := l.CreateRoom(roomCtx, "fixture", host)
	if err != nil {
		t.Fatalf("primera sala: no esperaba error, dio %v", err)
	}

	if _, _, err := l.CreateRoom(context.Background(), "fixture", host); err != ErrTooManyRooms {
		t.Fatalf("esperaba ErrTooManyRooms mientras la primera sigue viva, dio %v", err)
	}

	cancelRoom() // termina el goroutine de la primera sala (ctx.Done())
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("la sala no cerró tras cancelar su contexto")
	}

	// CreateRoom borra del mapa desde el propio goroutine que arrancó
	// (defer tras r.Run(ctx)) — puede haber una demora mínima entre
	// r.Done() y que lobby.rooms refleje el borrado.
	deadline := time.Now().Add(time.Second)
	for {
		_, _, err := l.CreateRoom(context.Background(), "fixture", host)
		if err == nil {
			return
		}
		if err != ErrTooManyRooms || time.Now().After(deadline) {
			t.Fatalf("esperaba poder crear una sala tras liberar capacidad, último error: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCreateRoom_MaxRoomsZeroMeansUnlimited(t *testing.T) {
	l := New(Config{CodeLength: 4, Room: room.Config{MaxPlayers: 2}}) // MaxRooms no seteado (0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := engine.PlayerInfo{ID: "p1", Name: "Ana"}

	for i := 0; i < 5; i++ {
		if _, _, err := l.CreateRoom(ctx, "fixture", host); err != nil {
			t.Fatalf("sala #%d: no esperaba error con MaxRooms=0, dio %v", i, err)
		}
	}
}
