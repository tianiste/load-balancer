package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Attempts int = iota
	Retry
)

func GetAttemptsFromContext(r *http.Request) int {
	if attempts, ok := r.Context().Value(Attempts).(int); ok {
		return attempts
	}
	return 0
}

func GetRetryFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(Retry).(int); ok {
		return retry
	}
	return 0
}

type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}

type ServerPool struct {
	backends []*Backend
	current  uint64
}

func (s *ServerPool) AddBackend(b *Backend) {
	s.backends = append(s.backends, b)
}

func (s *ServerPool) MarkBackendStatus(u *url.URL, alive bool) {
	for _, b := range s.backends {
		if b.URL.String() == u.String() {
			b.SetAlive(alive)
			return
		}
	}
}

func (s *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&s.current, uint64(1)) % uint64(len(s.backends)))
}

func (s *ServerPool) GetNextPeer() *Backend {
	if len(s.backends) == 0 {
		return nil
	}

	next := s.NextIndex()
	l := len(s.backends) + next
	for i := next; i < l; i++ {
		idx := i % len(s.backends)
		if s.backends[idx].IsAlive() {
			if i != next {
				atomic.StoreUint64(&s.current, uint64(idx))
			}
			return s.backends[idx]
		}
	}
	return nil
}

func (s *ServerPool) HealthCheck() {
	for _, b := range s.backends {
		status := "up"
		alive := isBackendAlive(b.URL)
		b.SetAlive(alive)
		if !alive {
			status = "down"
		}
		log.Printf("%s [%s]\n", b.URL, status)
	}
}

func isBackendAlive(u *url.URL) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		log.Println("Site unreachable, error:", err)
		return false
	}
	_ = conn.Close()
	return true
}

func healthCheck() {
	t := time.NewTicker(time.Second * 20)
	for {
		select {
		case <-t.C:
			log.Println("Starting health check...")
			serverPool.HealthCheck()
			log.Println("Health check completed")
		}
	}
}

var serverPool ServerPool

func lb(w http.ResponseWriter, r *http.Request) {
	attempts := GetAttemptsFromContext(r)
	if attempts > 3 {
		log.Printf("%s(%s) Max attempts reached, terminating\n", r.RemoteAddr, r.URL.Path)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	peer := serverPool.GetNextPeer()
	if peer != nil {
		peer.ReverseProxy.ServeHTTP(w, r)
		return
	}

	http.Error(w, "Service not available", http.StatusServiceUnavailable)
}

func newReverseProxy(serverURL *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(serverURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
	}

	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, e error) {
		log.Printf("[%s] %s\n", serverURL.Host, e.Error())

		retries := GetRetryFromContext(request)
		if retries < 3 {
			select {
			case <-time.After(10 * time.Millisecond):
				ctx := context.WithValue(request.Context(), Retry, retries+1)
				proxy.ServeHTTP(writer, request.WithContext(ctx))
			}
			return
		}

		serverPool.MarkBackendStatus(serverURL, false)

		attempts := GetAttemptsFromContext(request)
		log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)

		ctx := context.WithValue(request.Context(), Attempts, attempts+1)
		lb(writer, request.WithContext(ctx))
	}

	return proxy
}

func main() {
	var (
		port     int
		backends string
	)

	flag.IntVar(&port, "port", 3030, "port for the load balancer to listen on")
	flag.StringVar(&backends, "backends", "http://localhost:8080,http://localhost:8081",
		"comma-separated backend URLs (e.g. http://localhost:8080,http://localhost:8081)")
	flag.Parse()

	for _, raw := range strings.Split(backends, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		u, err := url.Parse(raw)
		if err != nil {
			log.Fatalf("Invalid backend URL %q: %v", raw, err)
		}

		b := &Backend{
			URL:          u,
			Alive:        true,
			ReverseProxy: newReverseProxy(u),
		}

		serverPool.AddBackend(b)
		log.Printf("Configured backend: %s\n", u.String())
	}

	if len(serverPool.backends) == 0 {
		log.Fatal("No backends configured. Provide -backends with at least one URL.")
	}

	go healthCheck()

	server := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(lb),
	}

	log.Printf("Load balancer started at :%d\n", port)
	log.Fatal(server.ListenAndServe())
}
