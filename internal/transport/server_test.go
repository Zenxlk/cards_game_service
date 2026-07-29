package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"

	"github.com/ZenXLK/cards_game_service/internal/auth"
	"github.com/ZenXLK/cards_game_service/internal/lobby"
	"github.com/ZenXLK/cards_game_service/internal/room"
)

// Estos tests cubren los guards de los endpoints nuevos (persistencia no
// configurada, autorización) sin depender de una Postgres real — el mismo
// enfoque que internal/room/room_test.go para JWT/JWKS.

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	l := lobby.New(lobby.Config{CodeLength: 6, Room: room.Config{MaxPlayers: 5}})
	return NewServer(context.Background(), l, cfg)
}

type transportAuthFixture struct {
	verifier *auth.Verifier
	secret   []byte
	kid      string
}

func newTransportAuthFixture(t *testing.T) *transportAuthFixture {
	t.Helper()
	ctx := context.Background()
	secret := []byte("test-hmac-secret")
	kid := "test-kid"

	jwk, err := jwkset.NewJWKFromKey(secret, jwkset.JWKOptions{
		Marshal: jwkset.JWKMarshalOptions{Private: true},
		Metadata: jwkset.JWKMetadataOptions{
			ALG: jwkset.AlgHS256,
			KID: kid,
			USE: jwkset.UseSig,
		},
	})
	if err != nil {
		t.Fatalf("jwkset.NewJWKFromKey: %v", err)
	}
	memStore := jwkset.NewMemoryStorage()
	if err := memStore.KeyWrite(ctx, jwk); err != nil {
		t.Fatalf("KeyWrite: %v", err)
	}
	jwksJSON, err := memStore.JSON(ctx)
	if err != nil {
		t.Fatalf("memStore.JSON: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	}))
	t.Cleanup(srv.Close)

	verifier, err := auth.NewVerifier(ctx, srv.URL)
	if err != nil {
		t.Fatalf("auth.NewVerifier: %v", err)
	}
	return &transportAuthFixture{verifier: verifier, secret: secret, kid: kid}
}

func (f *transportAuthFixture) sign(t *testing.T, sub string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header[jwkset.HeaderKID] = f.kid
	signed, err := token.SignedString(f.secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func TestHandleGetPlayer_NoStoreConfigured_Returns503(t *testing.T) {
	s := newTestServer(t, Config{})
	// uuid válido: si no lo fuera, ganaría el 400 de formato antes de
	// siquiera llegar a preguntarle al store si está configurado.
	req := httptest.NewRequest(http.MethodGet, "/players/44444444-4444-4444-4444-444444444444", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("esperaba 503, dio %d: %s", rec.Code, rec.Body)
	}
}

func TestHandleGetPlayer_MalformedID_Returns400(t *testing.T) {
	s := newTestServer(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/players/no-es-un-uuid", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 (id no es un uuid), dio %d: %s", rec.Code, rec.Body)
	}
}

func TestHandleLeaderboard_NoStoreConfigured_Returns503(t *testing.T) {
	s := newTestServer(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("esperaba 503, dio %d: %s", rec.Code, rec.Body)
	}
}

func TestHandleUpdateNickname_NoAuthHeader_Returns401(t *testing.T) {
	s := newTestServer(t, Config{})
	req := httptest.NewRequest(http.MethodPatch, "/players/p1/nickname", strings.NewReader(`{"nickname":"Ana"}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, dio %d: %s", rec.Code, rec.Body)
	}
}

func TestHandleUpdateNickname_TokenSubDoesNotMatchPathID_Returns401(t *testing.T) {
	fixture := newTransportAuthFixture(t)
	s := newTestServer(t, Config{Auth: fixture.verifier})

	tok := fixture.sign(t, "real-uuid-del-usuario")
	req := httptest.NewRequest(http.MethodPatch, "/players/otro-id/nickname", strings.NewReader(`{"nickname":"Ana"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 (sub distinto del id en la ruta), dio %d: %s", rec.Code, rec.Body)
	}
}

func TestHandleUpdateNickname_OversizedNickname_Returns400(t *testing.T) {
	fixture := newTransportAuthFixture(t)
	s := newTestServer(t, Config{Auth: fixture.verifier})

	const sub = "33333333-3333-3333-3333-333333333333"
	tok := fixture.sign(t, sub)
	body := `{"nickname":"` + strings.Repeat("x", room.MaxDisplayNameLen+1) + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/players/"+sub+"/nickname", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 (nickname demasiado largo), dio %d: %s", rec.Code, rec.Body)
	}
}
