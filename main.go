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
	"slices"
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

	var state App
	state.database = database.New(db)
	state.secret = os.Getenv("APP_SECRET")
	if state.secret == "" {
		slog.Error("APP_SECRET is not set")
		os.Exit(1)
	}
	state.polkaKey = os.Getenv("POLKA_API_KEY")

	chain := NewChain(logger, state.hit)

	fs := http.StripPrefix("/app", http.FileServer(http.Dir("app")))
	mux.Handle("/app/", chain.Then(fs))

	mux.HandleFunc("GET /api/healthz", health)
	mux.Handle("POST /api/users", chain.ThenFunc(state.CreateUser))
	mux.Handle("PUT /api/users", chain.ThenFunc(state.UpdateUser))
	mux.Handle("POST /api/login", chain.ThenFunc(state.Login))
	mux.Handle("POST /api/refresh", chain.ThenFunc(state.RefreshToken))
	mux.Handle("POST /api/revoke", chain.ThenFunc(state.RevokeToken))
	mux.Handle("POST /api/chirps", chain.ThenFunc(state.ChirpStore))
	mux.Handle("GET /api/chirps", chain.ThenFunc(state.ChirpIndex))
	mux.Handle("GET /api/chirps/{id}", chain.ThenFunc(state.ChirpShow))
	mux.Handle("DELETE /api/chirps/{id}", chain.ThenFunc(state.ChirpDelete))
	mux.Handle("POST /api/polka/webhooks", chain.ThenFunc(state.PolkaWebhook))

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
	for _, v := range slices.Backward(c.Middlewares) {
		next = v(next)
	}

	return next
}

func (c Chain) ThenFunc(next http.HandlerFunc) http.Handler {
	if next == nil {
		c.Then(nil)
	}

	return c.Then(http.HandlerFunc(next))
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

type App struct {
	secret   string
	polkaKey string
	hits     atomic.Int32
	database *database.Queries
}

func (a *App) Metrics(w http.ResponseWriter, r *http.Request) {
	tpl, err := template.ParseFiles("app/metrics.html")
	if err != nil {
		w.WriteHeader(500)
		return
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, map[string]any{
		"Hits": a.hits.Load(),
	}); err != nil {
		w.WriteHeader(500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

func (a *App) hit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.hits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (a *App) Reset(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("APP_ENV") != "local" {
		w.WriteHeader(403)
		return
	}

	a.hits.Store(0)

	if err := a.database.DeleteUsers(r.Context()); err != nil {
		slog.Error("failed to delete users", "err", err)
		w.WriteHeader(500)
		return
	}

	http.Redirect(w, r, "/admin/metrics", http.StatusSeeOther)
}

type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	IsChirpyRed bool      `json:"is_chirpy_red"`
}

func NewUser(u database.User) User {
	return User{
		ID:          u.ID,
		Email:       u.Email,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
		IsChirpyRed: u.IsChirpyRed,
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

func (a *App) CreateUser(w http.ResponseWriter, r *http.Request) {
	v, ok := bind[CreateUserRequest](w, r)
	if !ok {
		return
	}

	// not the prettiest code ..
	hash, err := auth.HashPassword(strings.TrimSpace(v.Password))
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	u, err := a.database.CreateUser(r.Context(), database.CreateUserParams{
		Email:          strings.ToLower(v.Email),
		HashedPassword: hash,
	})
	if err != nil {
		slog.Error("failed to create user", "err", err)
		w.WriteHeader(500)
		return
	}

	reply(w, 201, NewUser(u))
}

type UpdateUserRequest = CreateUserRequest

func (a *App) UpdateUser(w http.ResponseWriter, r *http.Request) {
	bearer, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID, err := auth.ValidateJWT(bearer, a.secret)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	v, ok := bind[UpdateUserRequest](w, r)
	if !ok {
		return
	}

	// not the prettiest code ..
	hash, err := auth.HashPassword(strings.TrimSpace(v.Password))
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	u, err := a.database.UpdateUser(r.Context(), database.UpdateUserParams{
		ID:             userID,
		Email:          strings.ToLower(v.Email),
		HashedPassword: hash,
	})
	if err != nil {
		slog.Error("failed to update user", "err", err)
		w.WriteHeader(500)
		return
	}

	reply(w, 201, NewUser(u))
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

func (a *App) Login(w http.ResponseWriter, r *http.Request) {
	v, ok := bind[LoginRequest](w, r)
	if !ok {
		return
	}

	u, err := a.database.GetUserByEmail(r.Context(), strings.ToLower(v.Email))
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	// not the prettiest code ..
	ok, err = auth.CheckPasswordHash(strings.TrimSpace(v.Password), u.HashedPassword)
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	if !ok {
		// prevent enumeration attack
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	token, err := auth.MakeJWT(u.ID, a.secret, time.Hour)
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	refreshToken := auth.MakeRefreshToken()

	_, err = a.database.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
		UserID:    u.ID,
		Token:     refreshToken,
		ExpiresAt: time.Now().UTC().Add(time.Hour * 24 * 60),
	})
	if err != nil {
		reply(w, 500, map[string]any{"error": "internal server error"})
		return
	}

	reply(w, 200, struct {
		User
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}{NewUser(u), token, refreshToken})
}

func (a *App) RefreshToken(w http.ResponseWriter, r *http.Request) {
	bearer, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	user, err := a.database.GetUserFromRefreshToken(r.Context(), bearer)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	accessToken, err := auth.MakeJWT(user.ID, a.secret, time.Hour)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal server error")
		return
	}

	reply(w, 200, struct {
		Token string `json:"token"`
	}{accessToken})
}

func (a *App) RevokeToken(w http.ResponseWriter, r *http.Request) {
	bearer, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	_, err = a.database.GetRefreshToken(r.Context(), bearer)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	now := time.Now().UTC()
	_, err = a.database.UpdateRefreshToken(r.Context(), database.UpdateRefreshTokenParams{
		RevokedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal server error")
		return
	}

	reply(w, 204, nil)
}

type CreateChirpRequest struct {
	Body   string `json:"body"`
	UserID string `json:"user_id,omitempty"` // ignored; the user comes from the JWT
}

func (r CreateChirpRequest) Validate() map[string]string {
	issues := make(map[string]string)

	_, err := NewChirp(r.Body)
	if err != nil {
		issues["email"] = "must be a valid email address"
	}

	return issues
}

func (a *App) ChirpStore(w http.ResponseWriter, r *http.Request) {
	bearer, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID, err := auth.ValidateJWT(bearer, a.secret)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	v, ok := bind[CreateChirpRequest](w, r)
	if !ok {
		return
	}

	e, _ := NewChirp(v.Body)
	c, err := a.database.CreateChirp(r.Context(), database.CreateChirpParams{
		Body:   e.Body,
		UserID: userID,
	})
	if err != nil {
		slog.Error("failed to create chirp", "err", err)
		w.WriteHeader(500)
		return
	}

	reply(w, 201, NewChirpFromDB(c))
}

func (a *App) ChirpIndex(w http.ResponseWriter, r *http.Request) {
	records, err := a.database.GetChirps(r.Context())
	if err != nil {
		slog.Error("failed to get chirps", "err", err)
		w.WriteHeader(500)
		return
	}

	collection := make([]Chirp, 0, len(records))
	for _, r := range records {
		collection = append(collection, NewChirpFromDB(r))
	}

	reply(w, 200, collection)
}

func (a *App) ChirpShow(w http.ResponseWriter, r *http.Request) {
	param := r.PathValue("id")
	id, err := uuid.Parse(param)
	if err != nil {
		reply(w, 400, map[string]any{"error": "invalid chirp id"})
		return
	}

	record, err := a.database.GetChirpByID(r.Context(), id)
	if err != nil {
		if errors.Is(sql.ErrNoRows, err) {
			reply(w, 404, map[string]any{"error": "not found"})
			return
		}

		slog.Error("failed to get chirp by id", "err", err, "id", id)
		w.WriteHeader(500)
		return
	}

	reply(w, 200, NewChirpFromDB(record))
}

func (a *App) ChirpDelete(w http.ResponseWriter, r *http.Request) {
	bearer, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID, err := auth.ValidateJWT(bearer, a.secret)
	if err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	param := r.PathValue("id")
	id, err := uuid.Parse(param)
	if err != nil {
		reply(w, 400, map[string]any{"error": "invalid chirp id"})
		return
	}

	record, err := a.database.GetChirpByID(r.Context(), id)
	if err != nil {
		if errors.Is(sql.ErrNoRows, err) {
			reply(w, 404, map[string]any{"error": "not found"})
			return
		}

		slog.Error("failed to get chirp by id", "err", err, "id", id)
		w.WriteHeader(500)
		return
	}

	if record.UserID != userID {
		fail(w, http.StatusForbidden, "forbidden")
		return
	}

	err = a.database.DeleteChirp(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete chirp", "err", err, "id", id)
		w.WriteHeader(500)
		return
	}

	reply(w, 204, nil)
}

type PolkaWebhookRequest struct {
	Event string `json:"event"`
	Data  struct {
		UserID uuid.UUID `json:"user_id"`
	} `json:"data"`
}

func (a *App) PolkaWebhook(w http.ResponseWriter, r *http.Request) {
	apiKey, err := auth.GetAPIKey(r.Header)
	if err != nil || apiKey != a.polkaKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req PolkaWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if req.Event != "user.upgraded" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = a.database.MarkChirpyRed(r.Context(), req.Data.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		slog.Error("failed to upgrade user to chirpy red", "err", err, "id", req.Data.UserID)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func reply(w http.ResponseWriter, code int, v any) {
	if code == http.StatusNoContent {
		w.WriteHeader(code)
		return
	}

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

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}

type Validator interface {
	Validate() map[string]string
}

func bind[T Validator](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			fail(w, http.StatusRequestEntityTooLarge, "request body too large")
			return v, false
		}

		fail(w, http.StatusBadRequest, "malformed request body")
		return v, false
	}

	if issues := v.Validate(); len(issues) > 0 {
		reply(w, http.StatusUnprocessableEntity, map[string]any{"errors": issues})
		return v, false
	}

	return v, true
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
