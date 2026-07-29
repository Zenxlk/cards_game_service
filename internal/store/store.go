// Package store persiste identidad de jugadores, historial de partidas y
// auditoría de seguridad/lobby en Postgres (Supabase). Las escrituras nunca
// deben bloquear al llamador — en la práctica, el goroutine de una Room —
// así que se encolan y las procesa un worker aparte; si la cola se llena,
// se descarta el registro y se loguea en vez de bloquear una partida.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// jobQueueSize acota cuánta escritura pendiente se tolera antes de empezar
// a descartar — suficiente para absorber un pico si Postgres está lento,
// sin acumular memoria sin límite si está caído.
const jobQueueSize = 256

// ErrNotConfigured lo devuelven las lecturas cuando se llaman sobre un
// *Store nil (persistencia no configurada, ej. sin DATABASE_URL).
var ErrNotConfigured = errors.New("persistencia no configurada")

// ErrNotFound envuelve pgx.ErrNoRows para que los llamadores (handlers
// HTTP) no necesiten importar pgx solo para distinguir "no existe" de
// cualquier otro error de base de datos.
var ErrNotFound = errors.New("no encontrado")

type job func(ctx context.Context, pool *pgxpool.Pool)

// Store es nil-safe en todos sus métodos: un *Store nil representa
// "sin persistencia configurada" (igual que auth.Verifier nil representa
// "sin Supabase configurado"), para que el server siga andando sin DB.
type Store struct {
	pool *pgxpool.Pool
	jobs chan job
}

// New conecta contra connString y arranca el worker de escritura async.
// ctx debe ser el de vida del proceso (el mismo que ya usa transport.Server
// para las salas) — el worker corre hasta que se cancele, y ahí cierra el
// pool.
func New(ctx context.Context, connString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, jobs: make(chan job, jobQueueSize)}
	go s.run(ctx)
	return s, nil
}

func (s *Store) run(ctx context.Context) {
	defer s.pool.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			j(ctx, s.pool)
		}
	}
}

func (s *Store) enqueue(j job) {
	if s == nil {
		return
	}
	select {
	case s.jobs <- j:
	default:
		slog.Warn("store: cola de escritura llena, se descarta un registro")
	}
}

// NewMatchID genera el id de una partida antes de que exista la fila en
// Postgres — lo decide la aplicación (no gen_random_uuid() en el INSERT)
// para que RecordMatchStart pueda ser fire-and-forget: nadie necesita
// esperar una respuesta de la DB para saber el id de la partida que
// recién arrancó.
func NewMatchID() string { return uuid.NewString() }

// UpsertPlayer registra o refresca la fila de un jugador autenticado
// (cuenta real o anónima vía Supabase Auth). Nunca se llama para
// invitados sin JWT — esos no tienen fila en players.
func (s *Store) UpsertPlayer(playerID string, isAnonymous bool) {
	s.enqueue(func(ctx context.Context, pool *pgxpool.Pool) {
		_, err := pool.Exec(ctx, `
			insert into players (id, is_anonymous, last_seen_at)
			values ($1, $2, now())
			on conflict (id) do update
			  set last_seen_at = now(), is_anonymous = excluded.is_anonymous
		`, playerID, isAnonymous)
		if err != nil {
			slog.Error("store: UpsertPlayer falló", "err", err, "playerId", playerID)
		}
	})
}

// RecordMatchStart deja constancia de que una partida arrancó. matchID lo
// genera el llamador con NewMatchID antes de invocar esto.
func (s *Store) RecordMatchStart(matchID, roomCode, gameType string) {
	s.enqueue(func(ctx context.Context, pool *pgxpool.Pool) {
		_, err := pool.Exec(ctx, `
			insert into matches (id, room_code, game_type, status, started_at)
			values ($1, $2, $3, 'in_progress', now())
		`, matchID, roomCode, gameType)
		if err != nil {
			slog.Error("store: RecordMatchStart falló", "err", err, "matchId", matchID)
		}
	})
}

// MatchPlayerResult es un participante autenticado de una partida ya
// terminada. Los invitados sin JWT no tienen fila en players, así que no
// pueden aparecer acá — insertar uno violaría la FK a players(id) a
// propósito: no queremos IDs arbitrarios de cliente mezclados con
// identidades reales.
type MatchPlayerResult struct {
	PlayerID    string
	DisplayName string
	IsHost      bool
	Placement   *int
	Eliminated  bool
}

// RecordMatchEnd cierra una partida: actualiza su estado y graba los
// participantes autenticados en una sola transacción.
func (s *Store) RecordMatchEnd(matchID, status string, winnerPlayerID *string, players []MatchPlayerResult) {
	s.enqueue(func(ctx context.Context, pool *pgxpool.Pool) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			slog.Error("store: RecordMatchEnd: begin falló", "err", err, "matchId", matchID)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `
			update matches set status = $2, ended_at = now(), winner_player_id = $3
			where id = $1
		`, matchID, status, winnerPlayerID); err != nil {
			slog.Error("store: RecordMatchEnd: update matches falló", "err", err, "matchId", matchID)
			return
		}

		for _, p := range players {
			var eliminatedAt *time.Time
			if p.Eliminated {
				now := time.Now()
				eliminatedAt = &now
			}
			if _, err := tx.Exec(ctx, `
				insert into match_players (match_id, player_id, display_name, is_host, placement, eliminated_at)
				values ($1, $2, $3, $4, $5, $6)
				on conflict (match_id, player_id) do update
				  set display_name = excluded.display_name,
				      placement = excluded.placement,
				      eliminated_at = excluded.eliminated_at
			`, matchID, p.PlayerID, p.DisplayName, p.IsHost, p.Placement, eliminatedAt); err != nil {
				slog.Error("store: RecordMatchEnd: insert match_players falló",
					"err", err, "matchId", matchID, "playerId", p.PlayerID)
				return
			}
		}

		if err := tx.Commit(ctx); err != nil {
			slog.Error("store: RecordMatchEnd: commit falló", "err", err, "matchId", matchID)
		}
	})
}

// RecordAuditEvent registra un evento de seguridad/lobby (no acciones de
// partida). authenticatedPlayerID va nil si la conexión no traía un JWT
// válido — nunca se manda ahí un playerId arbitrario de cliente, por la
// misma razón que en MatchPlayerResult.
func (s *Store) RecordAuditEvent(roomCode string, authenticatedPlayerID *string, eventType string, detail map[string]any) {
	s.enqueue(func(ctx context.Context, pool *pgxpool.Pool) {
		detailJSON, err := json.Marshal(detail)
		if err != nil {
			detailJSON = []byte("{}")
		}
		if _, err := pool.Exec(ctx, `
			insert into audit_events (room_code, player_id, event_type, detail)
			values ($1, $2, $3, $4)
		`, roomCode, authenticatedPlayerID, eventType, detailJSON); err != nil {
			slog.Error("store: RecordAuditEvent falló", "err", err, "eventType", eventType)
		}
	})
}

// PlayerProfile es el perfil público + estadísticas de un jugador
// autenticado, para GET /players/{id}.
type PlayerProfile struct {
	ID            string
	Nickname      *string
	IsAnonymous   bool
	CreatedAt     time.Time
	MatchesPlayed int
	Wins          int
}

// GetPlayerProfile es una lectura síncrona — la llaman los handlers HTTP,
// no el goroutine de una Room, así que no hay razón para encolarla.
func (s *Store) GetPlayerProfile(ctx context.Context, playerID string) (PlayerProfile, error) {
	if s == nil {
		return PlayerProfile{}, ErrNotConfigured
	}
	var p PlayerProfile
	err := s.pool.QueryRow(ctx, `
		select p.id, p.nickname, p.is_anonymous, p.created_at,
		       count(mp.match_id) as matches_played,
		       count(*) filter (where m.winner_player_id = p.id) as wins
		from players p
		left join match_players mp on mp.player_id = p.id
		left join matches m on m.id = mp.match_id
		where p.id = $1
		group by p.id
	`, playerID).Scan(&p.ID, &p.Nickname, &p.IsAnonymous, &p.CreatedAt, &p.MatchesPlayed, &p.Wins)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlayerProfile{}, ErrNotFound
	}
	if err != nil {
		return PlayerProfile{}, err
	}
	return p, nil
}

// LeaderboardEntry es una fila de GET /leaderboard.
type LeaderboardEntry struct {
	PlayerID string
	Nickname *string
	Wins     int
}

// GetLeaderboard trae los top jugadores por victorias. Tráfico bajo y una
// sola instancia: una consulta agregada en vivo alcanza, no hace falta
// materializar contadores que puedan desincronizarse de match_players.
func (s *Store) GetLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
		select p.id, p.nickname, count(*) as wins
		from players p
		join matches m on m.winner_player_id = p.id
		group by p.id
		order by wins desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LeaderboardEntry
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.PlayerID, &e.Nickname, &e.Wins); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// UpdateNickname es una escritura síncrona (a diferencia de las anteriores):
// la llama un handler HTTP que necesita confirmar éxito/error al cliente,
// no el goroutine de una Room.
func (s *Store) UpdateNickname(ctx context.Context, playerID, nickname string) error {
	if s == nil {
		return ErrNotConfigured
	}
	_, err := s.pool.Exec(ctx, `update players set nickname = $2 where id = $1`, playerID, nickname)
	return err
}
