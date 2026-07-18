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

	"github.com/ZenXLK/cards-game-service/internal/config"
	"github.com/ZenXLK/cards-game-service/internal/lobby"
	"github.com/ZenXLK/cards-game-service/internal/room"
	"github.com/ZenXLK/cards-game-service/internal/transport"

	_ "github.com/ZenXLK/cards-game-service/games/explodingkittens" // registra "exploding_kittens" en pkg/engine vía init()
)

func main() {
	cfg := config.Default()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	l := lobby.New(lobby.Config{
		CodeLength: cfg.RoomCodeLength,
		Room: room.Config{
			MaxPlayers:    cfg.MaxPlayersPerRoom,
			GraceDuration: cfg.GraceDuration,
		},
	})

	srv := transport.NewServer(ctx, l, transport.DefaultConfig())
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

	slog.Info("github.com/ZenXLK/cards-game-service escuchando", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("el servidor terminó con error", "err", err)
		os.Exit(1)
	}
}
