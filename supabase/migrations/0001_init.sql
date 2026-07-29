-- Esquema de persistencia: identidad de jugador entre partidas, historial y
-- auditoría de seguridad/lobby. Detalle de diseño en docs/PERSISTENCE.md.
--
-- players.id referencia auth.users(id): tanto jugadores anónimos
-- (Supabase Anonymous Sign-In) como cuentas reales tienen fila acá, ya que
-- ambos casos emiten un JWT válido con "sub" = auth.users.id.

create table players (
  id           uuid primary key references auth.users (id) on delete cascade,
  nickname     text,
  is_anonymous boolean not null default true,
  created_at   timestamptz not null default now(),
  last_seen_at timestamptz not null default now()
);

create table matches (
  id               uuid primary key default gen_random_uuid(),
  room_code        text not null,
  game_type        text not null,
  status           text not null check (status in ('in_progress', 'finished', 'abandoned')),
  started_at       timestamptz not null default now(),
  ended_at         timestamptz,
  winner_player_id uuid references players (id)
);
create index matches_room_code_idx on matches (room_code);

create table match_players (
  match_id      uuid not null references matches (id) on delete cascade,
  player_id     uuid not null references players (id) on delete cascade,
  display_name  text not null,
  is_host       boolean not null default false,
  placement     int,
  eliminated_at timestamptz,
  primary key (match_id, player_id)
);
create index match_players_player_id_idx on match_players (player_id);

create table audit_events (
  id          bigint generated always as identity primary key,
  occurred_at timestamptz not null default now(),
  room_code   text,
  player_id   uuid references players (id),
  event_type  text not null,
  detail      jsonb
);
create index audit_events_occurred_at_idx on audit_events (occurred_at);
create index audit_events_player_id_idx on audit_events (player_id);

-- RLS habilitado sin policies en las cuatro tablas: el backend siempre lee y
-- escribe con la service_role key (bypassea RLS por diseño). Esto es
-- defensa en profundidad — si alguna vez se filtra la anon key, estas
-- tablas quedan con cero acceso vía esa key, no dependemos de policies bien
-- escritas para lograrlo.
alter table players enable row level security;
alter table matches enable row level security;
alter table match_players enable row level security;
alter table audit_events enable row level security;
