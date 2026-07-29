// Package auth valida JWTs de Supabase (identidad de jugador entre
// partidas, tanto anónima como de cuenta real) contra el JWKS del proyecto.
// No hay nada propio acá: la verificación de firma/expiración corre por
// cuenta de keyfunc + golang-jwt, ambas mantenidas activamente.
package auth

import (
	"context"
	"errors"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken cubre cualquier motivo por el que un authToken no sirve:
// firma inválida, expirado, malformado, o Verifier nil (Supabase no
// configurado). El llamador no necesita distinguir el motivo — solo debe
// tratar la conexión como no autenticada.
var ErrInvalidToken = errors.New("token de autenticación inválido")

// Identity es lo único que el resto del server necesita de un JWT válido.
type Identity struct {
	PlayerID    string // sub del JWT: uuid de auth.users, sirve como engine.PlayerID
	IsAnonymous bool   // claim is_anonymous de Supabase (Anonymous Sign-In)
}

// Verifier valida JWTs contra el JWKS de un proyecto Supabase. Nil-safe: un
// *Verifier nil representa "Supabase no configurado" y rechaza todo token,
// para que el server siga funcionando en modo 100% invitado sin Supabase.
type Verifier struct {
	jwks keyfunc.Keyfunc
}

// NewVerifier arranca el refresco en background del JWK Set en jwksURL. El
// ctx debe ser el de vida del proceso (el mismo que ya usa transport.Server
// para las salas), no el de un request HTTP puntual — controla cuánto vive
// la goroutine de refresco de keyfunc.
func NewVerifier(ctx context.Context, jwksURL string) (*Verifier, error) {
	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, err
	}
	return &Verifier{jwks: k}, nil
}

// Verify valida la firma y expiración de tokenString. keyfunc resuelve la
// clave pública correcta por "kid" y refresca el JWK Set solo si aparece un
// kid desconocido (rate-limited), así que esto no pega a la red en el caso
// común.
func (v *Verifier) Verify(tokenString string) (Identity, error) {
	if v == nil || tokenString == "" {
		return Identity{}, ErrInvalidToken
	}

	token, err := jwt.Parse(tokenString, v.jwks.Keyfunc)
	if err != nil || !token.Valid {
		return Identity{}, ErrInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Identity{}, ErrInvalidToken
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, ErrInvalidToken
	}
	isAnonymous, _ := claims["is_anonymous"].(bool)

	return Identity{PlayerID: sub, IsAnonymous: isAnonymous}, nil
}
