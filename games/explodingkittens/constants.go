package explodingkittens

// GameConstants en Dart (core/constants/game_constants.dart).
const (
	MinPlayers = 2
	MaxPlayers = 5

	InitialHandSize      = 7
	DefuseCardsPerPlayer = 1

	// Composición del mazo base (sin Defuse ni Exploding Kittens).
	NopeCount         = 5
	AttackCount       = 4
	SkipCount         = 4
	FavorCount        = 4
	ShuffleCount      = 4
	SeeTheFutureCount = 5

	CatCardCount = 4 // por cada uno de los 5 tipos de gato

	// Ventana para jugar Nope antes de resolver la acción pendiente.
	NopeWindowMS = 3000

	// Segundos de grace period antes de eliminar a un jugador desconectado.
	ReconnectTimeoutSeconds = 60
)
