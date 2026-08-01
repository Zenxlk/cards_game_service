package transport

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter mantiene un token bucket por IP — el propio conjunto de IPs
// distintas que llegan al servidor es, en sí mismo, un vector de consumo de
// memoria si no se poda: cleanupLoop libera las que llevan más de ttl sin
// mandar ninguna request.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
	rate     rate.Limit
	burst    int
	ttl      time.Duration
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPRateLimiter(r rate.Limit, burst int) *ipRateLimiter {
	l := &ipRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		rate:     r,
		burst:    burst,
		ttl:      10 * time.Minute,
	}
	go l.cleanupLoop()
	return l
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.limiters[ip]
	if !ok {
		entry = &rateLimiterEntry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

func (l *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(l.ttl)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-l.ttl)
		l.mu.Lock()
		for ip, entry := range l.limiters {
			if entry.lastSeen.Before(cutoff) {
				delete(l.limiters, ip)
			}
		}
		l.mu.Unlock()
	}
}

// rateLimitMiddleware envuelve next con un límite por IP, aplicado a todas
// las rutas por igual (crear sala, leaderboard, y el handshake inicial de
// WebSocket — nunca a los mensajes dentro de una partida ya conectada, esos
// van por el canal propio de la sala, no vuelven a pasar por acá). Cada
// sala creada de más es un goroutine con su propio estado; en un proceso
// con memoria acotada (p. ej. una VM chica) una ráfaga sin este límite
// puede agotarla antes de que LobbyIdleTimeout llegue a limpiar nada.
func rateLimitMiddleware(limiter *ipRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /healthz queda afuera: algunas plataformas/monitoreo lo pegan
		// seguido desde la misma IP, y no crea ningún recurso — no es el
		// endpoint que este límite necesita proteger.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.allow(clientIP(r)) {
			http.Error(w, "demasiadas requests, esperá un momento", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP prefiere X-Forwarded-For (la IP real del cliente, si el proceso
// corre detrás de un proxy que la setee) sobre RemoteAddr. Esto asume que
// el proxy es el único camino hacia el puerto de la app — si el puerto
// quedara expuesto directo a internet sin pasar por él, cualquiera podría
// falsear este header y esquivar el límite por completo. El despliegue de
// referencia (ver docs/DEPLOYMENT.md) ya cumple esa condición: el
// contenedor de la app no publica su puerto al host, solo es alcanzable
// desde el proxy en la misma red de Docker.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			fwd = fwd[:i]
		}
		if ip := strings.TrimSpace(fwd); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
