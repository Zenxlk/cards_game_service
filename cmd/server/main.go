// cmd/server wire-up: config, lobby, transport HTTP/WS. La única
// responsabilidad de este paquete es ensamblar las piezas y decidir qué
// motores de juego incluye el binario — el resto del server no sabe qué
// juegos existen.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZenXLK/cards_game_service/internal/auth"
	"github.com/ZenXLK/cards_game_service/internal/config"
	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
	"github.com/ZenXLK/cards_game_service/internal/store"
	"github.com/ZenXLK/cards_game_service/internal/transport"

	_ "github.com/ZenXLK/cards_game_service/games/explodingkittens" // registra "exploding_kittens" en pkg/engine vía init()
)

func main() {
	cfg := config.Default()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// authVerifier y persistStore quedan nil si no se configuró Supabase —
	// room.Config y transport.Config son nil-safe para ambos, así que el
	// server sigue funcionando 100% invitado y sin persistencia (igual que
	// antes de esta feature) sin ninguna rama especial acá.
	var authVerifier *auth.Verifier
	if cfg.SupabaseJWKSURL != "" {
		v, err := auth.NewVerifier(ctx, cfg.SupabaseJWKSURL)
		if err != nil {
			slog.Error("no se pudo inicializar el verificador de JWT de Supabase", "err", err)
			os.Exit(1)
		}
		authVerifier = v
	}

	var persistStore *store.Store
	if cfg.DatabaseURL != "" {
		st, err := store.New(ctx, cfg.DatabaseURL)
		if err != nil {
			slog.Error("no se pudo conectar a la base de datos de persistencia", "err", err)
			os.Exit(1)
		}
		persistStore = st
	}

	l := lobby.New(lobby.Config{
		CodeLength: cfg.RoomCodeLength,
		Room: room.Config{
			MaxPlayers:       cfg.MaxPlayersPerRoom,
			GraceDuration:    cfg.GraceDuration,
			LobbyIdleTimeout: cfg.LobbyIdleTimeout,
			Auth:             authVerifier,
			Store:            persistStore,
		},
	})

	transportCfg := transport.DefaultConfig()
	transportCfg.Store = persistStore
	transportCfg.Auth = authVerifier
	srv := transport.NewServer(ctx, l, transportCfg)
	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("github.com/ZenXLK/cards_game_service escuchando", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("el servidor terminó con error", "err", err)
		os.Exit(1)
	}
}
