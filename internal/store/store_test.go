package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Estos tests cubren la cola async (enqueue/drop) y la nil-safety, sin una
// Postgres real. Las consultas SQL en sí (UpsertPlayer, RecordMatchEnd,
// etc.) no tienen cobertura automática todavía — necesitan una instancia
// real de Postgres, pendiente de sumar como servicio de CI.

func TestNilStore_WriteMethodsAreNoop(t *testing.T) {
	var s *Store
	s.UpsertPlayer("p1", true)
	s.RecordMatchStart("m1", "ROOM1", "exploding_kittens")
	s.RecordMatchEnd("m1", "finished", nil, nil)
	s.RecordAuditEvent("ROOM1", nil, "room_created", nil)
	// Ninguna debe paniquear ni bloquear.
}

func TestNilStore_ReadMethodsReturnErrNotConfigured(t *testing.T) {
	var s *Store
	if _, err := s.GetPlayerProfile(context.Background(), "p1"); err != ErrNotConfigured {
		t.Fatalf("GetPlayerProfile: esperaba ErrNotConfigured, dio %v", err)
	}
	if _, err := s.GetLeaderboard(context.Background(), 10); err != ErrNotConfigured {
		t.Fatalf("GetLeaderboard: esperaba ErrNotConfigured, dio %v", err)
	}
	if err := s.UpdateNickname(context.Background(), "p1", "Ana"); err != ErrNotConfigured {
		t.Fatalf("UpdateNickname: esperaba ErrNotConfigured, dio %v", err)
	}
}

func TestEnqueue_DropsExcessJobsWhenQueueFull(t *testing.T) {
	s := &Store{jobs: make(chan job, 1)}

	// Ocupa el único slot; nadie consume la cola en este test.
	s.enqueue(func(context.Context, *pgxpool.Pool) {})
	for i := 0; i < 5; i++ {
		s.enqueue(func(context.Context, *pgxpool.Pool) {})
	}

	if got := len(s.jobs); got != 1 {
		t.Fatalf("esperaba que la cola se quedara en 1 job (el resto descartado), quedaron %d", got)
	}
}

func TestEnqueue_NeverBlocksWhenQueueFull(t *testing.T) {
	s := &Store{jobs: make(chan job, 1)}
	s.enqueue(func(context.Context, *pgxpool.Pool) {}) // llena el único slot

	done := make(chan struct{})
	go func() {
		s.enqueue(func(context.Context, *pgxpool.Pool) {}) // debe descartarse, nunca bloquear
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue bloqueó con la cola llena — esto bloquearía el goroutine de una Room")
	}
}
