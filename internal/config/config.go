// Package config centraliza los valores configurables del servidor. Los
// defaults calcan las constantes ya probadas en el cliente Flutter
// (GameConstants) para que el comportamiento no cambie al migrar de LAN a
// online.
package config

import (
	"os"
	"time"
)

type Config struct {
	Addr string // ej. ":8080"

	MaxPlayersPerRoom int
	RoomCodeLength    int

	// GraceDuration: segundos de reconexión antes de eliminar a un jugador
	// desconectado. GameConstants.reconnectTimeoutSeconds = 60 en Dart.
	GraceDuration time.Duration

	// LobbyIdleTimeout: cuánto puede vivir una sala sin arrancar la partida
	// antes de cerrarse sola — sin esto, POST /rooms sin uso real deja
	// goroutines y memoria vivos indefinidamente (no tiene equivalente en
	// Dart: WsServer no existe hasta que alguien lo crea localmente).
	LobbyIdleTimeout time.Duration
}

func Default() Config {
	port := os.Getenv("PORT") // Cloud Run inyecta $PORT; ":8080" es el fallback para correr local
	if port == "" {
		port = "8080"
	}
	return Config{
		Addr:              ":" + port,
		MaxPlayersPerRoom: 5,
		RoomCodeLength:    6,
		GraceDuration:     60 * time.Second,
		LobbyIdleTimeout:  10 * time.Minute,
	}
}
