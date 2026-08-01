package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIP_PrefersXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345" // la IP interna del proxy, no la real
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")

	if got := clientIP(req); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, quería la primera IP de X-Forwarded-For", got)
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:54321"

	if got := clientIP(req); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, quería 203.0.113.7 (de RemoteAddr)", got)
	}
}

func TestIPRateLimiter_BlocksAfterBurstExhausted(t *testing.T) {
	l := newIPRateLimiter(1, 3) // 1 req/s, ráfaga de 3

	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request #%d dentro de la ráfaga debería pasar", i)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("la request que excede la ráfaga debería bloquearse")
	}

	// Una IP distinta tiene su propio balde, no se ve afectada.
	if !l.allow("5.6.7.8") {
		t.Fatal("una IP distinta no debería compartir el límite de otra")
	}
}

func TestRateLimitMiddleware_Returns429WhenBlocked(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimitMiddleware(l, next)

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/rooms", nil)
		r.RemoteAddr = "9.9.9.9:1"
		return r
	}

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req())
	if rec1.Code != http.StatusOK {
		t.Fatalf("primera request: esperaba 200, dio %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req())
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("segunda request (excede la ráfaga de 1): esperaba 429, dio %d", rec2.Code)
	}
	if called != 1 {
		t.Fatalf("el handler siguiente no debería haber corrido en la request bloqueada, corrió %d veces", called)
	}
}

func TestRateLimitMiddleware_HealthzNeverBlocked(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimitMiddleware(l, next)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "9.9.9.9:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/healthz nunca debería limitarse, intento #%d dio %d", i, rec.Code)
		}
	}
}

// TestIPRateLimiter_CleanupRemovesStaleEntries prueba la condición de poda
// (entradas más viejas que ttl se borran), no el goroutine cleanupLoop en
// sí — su ticker queda fijado al ttl que tenía el limiter al arrancar, sin
// forma determinista de forzar un ciclo real sin esperar ese intervalo
// completo. Por eso se construye el struct directo (sin newIPRateLimiter,
// que arrancaría ese goroutine) y se ejercita la misma lógica de poda que
// usa cleanupLoop.
func TestIPRateLimiter_CleanupRemovesStaleEntries(t *testing.T) {
	l := &ipRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		rate:     1,
		burst:    1,
		ttl:      20 * time.Millisecond,
	}

	l.allow("1.2.3.4")
	if len(l.limiters) != 1 {
		t.Fatal("esperaba una entrada tras la primera request")
	}

	time.Sleep(30 * time.Millisecond)

	cutoff := time.Now().Add(-l.ttl)
	for ip, entry := range l.limiters {
		if entry.lastSeen.Before(cutoff) {
			delete(l.limiters, ip)
		}
	}

	if len(l.limiters) != 0 {
		t.Fatalf("esperaba que la entrada vieja se podara, quedaron %d", len(l.limiters))
	}
}
