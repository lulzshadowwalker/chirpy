package main

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

func main() {
	mux := http.NewServeMux()
	var state State

	chain := NewChain(logger, state.hit)

	fs := http.StripPrefix("/app", http.FileServer(http.Dir("app")))
	mux.Handle("/app", chain.Then(fs))

	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /metrics", state.Metrics)
	mux.HandleFunc("POST /reset", state.Reset)

	s := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		if err := s.ListenAndServe(); err != nil && errors.Is(http.ErrServerClosed, err) {
			slog.Error("failed to start server", "err", err)
		}
	}()

	slog.Info("server started", "port", "8080")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("incoming request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

type Middleware func(next http.Handler) http.Handler

type Chain struct {
	Middlewares []Middleware
}

func NewChain(middlewares ...Middleware) Chain {
	return Chain{middlewares}
}

func (c Chain) Then(next http.Handler) http.Handler {
	if next == nil {
		next = http.DefaultServeMux
	}

	// reverse order such that the first middleware passed in is executed first
	for i := len(c.Middlewares) - 1; i >= 0; i-- {
		next = c.Middlewares[i](next)
	}

	return next
}

func (c Chain) ThenFunc(next http.HandlerFunc) http.Handler {
	if next == nil {
		c.Then(nil)
	}

	return c.Then(http.HandlerFunc(next))
}

type State struct {
	hits atomic.Int32
}

func (s *State) Metrics(w http.ResponseWriter, r *http.Request) {
	tpl, err := template.ParseFiles("app/metrics.html")
	if err != nil {
		w.WriteHeader(500)
		return
	}

	if err := tpl.Execute(w, map[string]any{
		"Hits": s.hits.Load(),
	}); err != nil {
		w.WriteHeader(500)
		return
	}
}

func (s *State) hit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		s.hits.Add(1)
	})
}

func (s *State) Reset(w http.ResponseWriter, r *http.Request) {
	s.hits.Store(0)
	http.Redirect(w, r, "/metrics", http.StatusSeeOther)
}
