package engine

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Factory construye una instancia de GameEngine a partir de la
// configuración específica del juego (p. ej. reglas de casa, semilla).
type Factory func(config json.RawMessage) (GameEngine, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register asocia un nombre de juego con su Factory. Cada implementación
// concreta se registra desde su propio init(), al estilo de
// database/sql.Register — el binario final decide qué juegos incluye con
// sus imports (blank imports en cmd/server), sin que este paquete ni el
// resto del servidor necesiten conocerlos.
//
// Entra en pánico ante un nombre duplicado: es un error de programación
// (dos juegos registrándose con el mismo nombre), no una condición de
// runtime a recuperar.
func Register(gameType string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[gameType]; exists {
		panic(fmt.Sprintf("engine: game type %q ya registrado", gameType))
	}
	factories[gameType] = factory
}

// Lookup devuelve la Factory registrada para gameType, si existe.
func Lookup(gameType string) (Factory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := factories[gameType]
	return f, ok
}
