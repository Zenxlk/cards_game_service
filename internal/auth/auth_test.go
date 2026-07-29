package auth

import (
	"context"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// testVerifier arma un Verifier respaldado por una JWK Set en memoria (HMAC)
// en vez de un servidor JWKS real por HTTP — la lógica que probamos es
// Verify, no el transporte de keyfunc (ya cubierto por sus propios tests).
func testVerifier(t *testing.T, secret []byte, keyID string) *Verifier {
	t.Helper()
	ctx := context.Background()

	jwk, err := jwkset.NewJWKFromKey(secret, jwkset.JWKOptions{
		Marshal: jwkset.JWKMarshalOptions{Private: true},
		Metadata: jwkset.JWKMetadataOptions{
			ALG: jwkset.AlgHS256,
			KID: keyID,
			USE: jwkset.UseSig,
		},
	})
	if err != nil {
		t.Fatalf("jwkset.NewJWKFromKey: %v", err)
	}

	store := jwkset.NewMemoryStorage()
	if err := store.KeyWrite(ctx, jwk); err != nil {
		t.Fatalf("store.KeyWrite: %v", err)
	}

	k, err := keyfunc.New(keyfunc.Options{Ctx: ctx, Storage: store})
	if err != nil {
		t.Fatalf("keyfunc.New: %v", err)
	}
	return &Verifier{jwks: k}
}

func signToken(t *testing.T, secret []byte, keyID string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header[jwkset.HeaderKID] = keyID
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func TestVerify_ValidToken_ReturnsIdentity(t *testing.T) {
	secret := []byte("test-secret")
	v := testVerifier(t, secret, "kid-1")

	tok := signToken(t, secret, "kid-1", jwt.MapClaims{
		"sub":          "5f1c9c1e-2b3a-4a3e-9c1e-2b3a4a3e9c1e",
		"is_anonymous": true,
		"exp":          time.Now().Add(time.Hour).Unix(),
	})

	got, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.PlayerID != "5f1c9c1e-2b3a-4a3e-9c1e-2b3a4a3e9c1e" || !got.IsAnonymous {
		t.Fatalf("Identity inesperada: %+v", got)
	}
}

func TestVerify_ExpiredToken_Rejected(t *testing.T) {
	secret := []byte("test-secret")
	v := testVerifier(t, secret, "kid-1")

	tok := signToken(t, secret, "kid-1", jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	if _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("esperaba ErrInvalidToken, dio %v", err)
	}
}

func TestVerify_WrongSigningKey_Rejected(t *testing.T) {
	v := testVerifier(t, []byte("real-secret"), "kid-1")

	tok := signToken(t, []byte("otro-secreto-cualquiera"), "kid-1", jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("esperaba ErrInvalidToken, dio %v", err)
	}
}

func TestVerify_NilVerifierOrEmptyToken_Rejected(t *testing.T) {
	var v *Verifier
	if _, err := v.Verify("cualquier-cosa"); err != ErrInvalidToken {
		t.Fatalf("Verifier nil: esperaba ErrInvalidToken, dio %v", err)
	}

	real := testVerifier(t, []byte("s"), "kid-1")
	if _, err := real.Verify(""); err != ErrInvalidToken {
		t.Fatalf("token vacío: esperaba ErrInvalidToken, dio %v", err)
	}
}

func TestVerify_MissingSubClaim_Rejected(t *testing.T) {
	secret := []byte("test-secret")
	v := testVerifier(t, secret, "kid-1")

	tok := signToken(t, secret, "kid-1", jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("esperaba ErrInvalidToken, dio %v", err)
	}
}
