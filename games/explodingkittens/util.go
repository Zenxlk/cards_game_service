package explodingkittens

import (
	"crypto/rand"
	"encoding/hex"
)

func newGameID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read no falla en la práctica en ningún target soportado
	return hex.EncodeToString(b)
}
