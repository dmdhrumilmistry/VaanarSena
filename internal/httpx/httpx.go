// Package httpx holds small HTTP helpers shared by the API and drivers.
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// trustProxy controls whether X-Forwarded-For is honoured.
var trustProxy atomic.Bool

// TrustProxy enables X-Forwarded-For handling. Enable it only behind a proxy
// that overwrites the header, or clients can spoof their audit-log address.
func TrustProxy(v bool) { trustProxy.Store(v) }

// ClientIP returns the caller's address for logging and rate limiting.
func ClientIP(r *http.Request) string {
	if trustProxy.Load() {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			return strings.TrimSpace(strings.Split(f, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// JSON writes v as a JSON response.
func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": msg}.
func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]string{"error": msg})
}
