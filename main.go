package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/lulzshadowwalker/chirpy/internal/auth"
	"github.com/lulzshadowwalker/chirpy/internal/database"
)

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("failed to load .env file", "err", err)
	}

	db, err := sql.Open("postgres", os.Getenv("DB_URL"))
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}

	if err := db.Ping(); err != nil {
		slog.Error("failed to ping database", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	var state State
	state.database = database.New(db)

	chain := NewChain(logger, state.hit)

	fs := http.StripPrefix("/app", http.FileServer(http.Dir("app")))
	mux.Handle("/app/", chain.Then(fs))

	mux.HandleFunc("GET /api/healthz", health)
	mux.Handle("POST /api/users", chain.ThenFunc(state.CreateUser))
	mux.Handle("POST /api/login", chain.ThenFunc(state.Login))
	mux.Handle("POST /api/chirps", chain.ThenFunc(state.CreateChirp))
	mux.Handle("GET /api/chirps", chain.ThenFunc(state.GetChirps))
	mux.Handle("GET /api/chirps/{id}", chain.ThenFunc(state.GetChirp))

	mux.HandleFunc("GET /admin/metrics", state.Metrics)
	mux.HandleFunc("POST /admin/reset", state.Reset)

	s := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	hits     atomic.Int32
	database *database.Queries
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
	if os.Getenv("APP_ENV") != "local" {
		w.WriteHeader(403)
		return
	}

	s.hits.Store(0)

	if err := s.database.DeleteUsers(r.Context()); err != nil {
		slog.Error("failed to delete users", "err", err)
		w.WriteHeader(500)
		return
	}

	http.Redirect(w, r, "/admin/metrics", http.StatusSeeOther)
}

var banned = map[string]struct{}{
	"kerfuffle": {},
	"sharbert":  {},
	"fornax":    {},
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

type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewUser(u database.User) User {
	return User{
		ID:        u.ID,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	Body      string    `json:"email"`
	UserID    uuid.UUID `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewChirp(s string) (*Chirp, error) {
	if utf8.RuneCountInString(s) > 140 {
		return nil, errors.New("chirp is too long")
	}

	return &Chirp{Body: scrub(s)}, nil
}

func NewChirpFromDB(c database.Chirp) Chirp {
	return Chirp{
		ID:        c.ID,
		Body:      c.Body,
		UserID:    c.UserID,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

type Validator interface {
	Validate() map[string]string
}

func decode[T Validator](r *http.Request) (T, map[string]string, error) {
	var v T
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return v, nil, err
	}

	return v, v.Validate(), nil
}

type CreateUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r CreateUserRequest) Validate() map[string]string {
	issues := make(map[string]string)

	// NOTE: Bob <bob@example.com> is a valid email when using `mail.ParseAddress`
	if addr, err := mail.ParseAddress(r.Email); err != nil || addr.Address != r.Email {
		issues["email"] = "must be a valid email address"
	}

	if len(r.Password) < 8 {
		issues["password"] = "must be at least 8 characters"
	}

	return issues
}

func (s *State) CreateUser(w http.ResponseWriter, r *http.Request) {
	v, issues, err := decode[CreateUserRequest](r)
	if err != nil {
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	if len(issues) != 0 {
		jason(w, 400, issues)
		return
	}

	// not the prettiest code ..
	hash, err := auth.HashPassword(strings.TrimSpace(v.Password))
	if err != nil {
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	u, err := s.database.CreateUser(r.Context(), database.CreateUserParams{
		Email:          strings.ToLower(v.Email),
		HashedPassword: hash,
	})
	if err != nil {
		slog.Error("failed to create user", "err", err)
		w.WriteHeader(500)
		return
	}

	jason(w, 201, NewUser(u))
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r LoginRequest) Validate() map[string]string {
	issues := make(map[string]string)

	// NOTE: Bob <bob@example.com> is a valid email when using `mail.ParseAddress`
	if addr, err := mail.ParseAddress(r.Email); err != nil || addr.Address != r.Email {
		issues["email"] = "must be a valid email address"
	}

	if len(r.Password) < 8 {
		issues["password"] = "must be at least 8 characters"
	}

	return issues
}

func (s *State) Login(w http.ResponseWriter, r *http.Request) {
	v, issues, err := decode[LoginRequest](r)
	if err != nil {
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	if len(issues) != 0 {
		jason(w, 400, issues)
		return
	}

	u, err := s.database.GetUserByEmail(r.Context(), strings.ToLower(v.Email))
	if err != nil {
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	// not the prettiest code ..
	ok, err := auth.CheckPasswordHash(strings.TrimSpace(v.Password), u.HashedPassword)
	if err != nil {
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	if !ok {
		// prevent enumeration attack
		jason(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	jason(w, 201, NewUser(u))
}

func (s *State) CreateChirp(w http.ResponseWriter, r *http.Request) {
	var params struct {
		Body   string `json:"body"`
		UserID string `json:"user_id"`
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&params); err != nil {
		jason(w, 400, map[string]any{"error": "something went wrong"})
		return
	}

	e, err := NewChirp(params.Body)
	if err != nil {
		jason(w, 400, map[string]any{"error": "invalid email address"})
		return
	}

	uid, err := uuid.Parse(params.UserID)
	if err != nil {
		jason(w, 400, map[string]any{"error": "invalid user id"})
		return
	}

	c, err := s.database.CreateChirp(r.Context(), database.CreateChirpParams{Body: e.Body, UserID: uid})
	if err != nil {
		slog.Error("failed to create chirp", "err", err)
		w.WriteHeader(500)
		return
	}

	jason(w, 201, NewChirpFromDB(c))
}

func (s *State) GetChirps(w http.ResponseWriter, r *http.Request) {
	records, err := s.database.GetChirps(r.Context())
	if err != nil {
		slog.Error("failed to get chirps", "err", err)
		w.WriteHeader(500)
		return
	}

	collection := make([]Chirp, 0, len(records))
	for _, r := range records {
		collection = append(collection, NewChirpFromDB(r))
	}

	jason(w, 200, collection)
}

func (s *State) GetChirp(w http.ResponseWriter, r *http.Request) {
	param := r.PathValue("id")
	id, err := uuid.Parse(param)
	if err != nil {
		jason(w, 400, map[string]any{"error": "invalid user id"})
		return
	}

	record, err := s.database.GetChirpByID(r.Context(), id)
	if err != nil {
		if errors.Is(sql.ErrNoRows, err) {
			jason(w, 404, map[string]any{"error": "not found"})
			return
		}

		slog.Error("failed to get chirp by id", "err", err, "id", id)
		w.WriteHeader(500)
		return
	}

	jason(w, 200, NewChirpFromDB(record))
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
