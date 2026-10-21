package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode/utf8"
)

func main() {
	mux := http.NewServeMux()
	var state State

	chain := NewChain(logger, state.hit)

	fs := http.StripPrefix("/app", http.FileServer(http.Dir("app")))
	mux.Handle("/app/", chain.Then(fs))

	mux.HandleFunc("GET /api/healthz", health)
	mux.HandleFunc("POST /api/validate_chirp", validateChirp)
	mux.HandleFunc("POST /api/chirps/validate", validateChirp)

	mux.HandleFunc("GET /admin/metrics", state.Metrics)
	mux.HandleFunc("POST /admin/reset", state.Reset)

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

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, map[string]any{
		"Hits": s.hits.Load(),
	}); err != nil {
		w.WriteHeader(500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

func (s *State) hit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (s *State) Reset(w http.ResponseWriter, r *http.Request) {
	s.hits.Store(0)
	http.Redirect(w, r, "/admin/metrics", http.StatusSeeOther)
}

var banned = map[string]struct{}{
	"kerfuffle": {},
	"sharbert":  {},
	"fornax":    {},
}

func validateChirp(w http.ResponseWriter, r *http.Request) {
	var params struct {
		Body string `json:"body"`
	}

	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		slog.Error("failed to decode body", "err", err)
		jason(w, 400, map[string]any{"error": "Something went wrong"})
		return
	}

	if utf8.RuneCountInString(params.Body) > 140 {
		jason(w, 400, map[string]any{"error": "Chirp is too long"})
		return
	}

	jason(w, 200, map[string]any{"cleaned_body": scrub(params.Body)})
}

func scrub(s string) string {
	words := strings.Split(s, " ")
	for i, word := range words {
		if _, ok := banned[strings.ToLower(word)]; ok {
			words[i] = "****"
		}
	}

	return strings.Join(words, " ")
}

func jason(w http.ResponseWriter, code int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		slog.Error("failed to encode response", "err", err)
		w.WriteHeader(500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	buf.WriteTo(w)
}
