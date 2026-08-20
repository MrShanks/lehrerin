package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Account is a teacher's login identity. Their planner data lives in a
// separate per-account Store, so every teacher's schedule, agendas, notes,
// and settings are completely isolated from one another.
type Account struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}

const sessionCookieName = "lehrerin_session"

var (
	ErrUsernameTaken      = errors.New("that username is already taken")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInviteCodeRequired = errors.New("invite code is required")
)

// AccountManager owns the account roster, the signed-session secret, and a
// cache of per-account Stores (each backed by its own JSON file on disk).
type AccountManager struct {
	mu           sync.RWMutex
	accountsPath string
	usersDir     string
	accounts     []Account
	sessionKey   []byte
	stores       map[string]*Store
	// inviteCode gates signups: if set, it must be supplied to create an
	// account, so only people who know it (i.e. people you shared it with)
	// can register.
	inviteCode string
}

func newAccountManager(baseDir string) *AccountManager {
	m := &AccountManager{stores: make(map[string]*Store), inviteCode: os.Getenv("SIGNUP_INVITE_CODE")}
	if baseDir == "" {
		m.sessionKey = randomBytes(32)
		return m
	}
	m.accountsPath = filepath.Join(baseDir, "accounts.json")
	m.usersDir = filepath.Join(baseDir, "users")
	if contents, err := os.ReadFile(m.accountsPath); err == nil {
		_ = json.Unmarshal(contents, &m.accounts)
	}
	m.sessionKey = loadOrCreateSessionKey(filepath.Join(baseDir, "session.key"))
	return m
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return buf
}

func loadOrCreateSessionKey(path string) []byte {
	if contents, err := os.ReadFile(path); err == nil && len(contents) == 32 {
		return contents
	}
	key := randomBytes(32)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, key, 0o600)
	}
	return key
}

// signUp creates a new account with its own blank planner store. If an
// invite code is configured, the caller must supply the matching code.
func (m *AccountManager) signUp(username, password, inviteCode string) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, errors.New("username is required")
	}
	if len(password) < 8 {
		return Account{}, errors.New("password must be at least 8 characters")
	}
	if m.inviteCode != "" && !hmac.Equal([]byte(inviteCode), []byte(m.inviteCode)) {
		return Account{}, ErrInviteCodeRequired
	}

	m.mu.Lock()
	for _, account := range m.accounts {
		if strings.EqualFold(account.Username, username) {
			m.mu.Unlock()
			return Account{}, ErrUsernameTaken
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		m.mu.Unlock()
		return Account{}, err
	}
	account := Account{ID: hex.EncodeToString(randomBytes(8)), Username: username, PasswordHash: string(hash)}
	m.accounts = append(m.accounts, account)
	err = m.persistAccountsLocked()
	m.mu.Unlock()
	if err != nil {
		return Account{}, err
	}

	store := m.storeFor(account.ID)
	store.mu.Lock()
	store.data.Teacher = account.Username
	err = store.persistLocked()
	store.mu.Unlock()
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *AccountManager) authenticate(username, password string) (Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, account := range m.accounts {
		if strings.EqualFold(account.Username, username) {
			if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
				return Account{}, ErrInvalidCredentials
			}
			return account, nil
		}
	}
	// Run a hash comparison anyway so the response time doesn't reveal
	// whether the username exists.
	bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), []byte(password))
	return Account{}, ErrInvalidCredentials
}

func (m *AccountManager) persistAccountsLocked() error {
	if m.accountsPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.accountsPath), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(m.accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.accountsPath, contents, 0o600)
}

// storeFor returns (creating and caching if necessary) the Store that holds
// one account's planner data, completely separate from every other account.
func (m *AccountManager) storeFor(accountID string) *Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	if store, ok := m.stores[accountID]; ok {
		return store
	}
	var path string
	if m.usersDir != "" {
		path = filepath.Join(m.usersDir, accountID+".json")
	}
	store := newStore(path)
	m.stores[accountID] = store
	return store
}

func (m *AccountManager) sessionCookieValue(accountID string) string {
	mac := hmac.New(sha256.New, m.sessionKey)
	mac.Write([]byte(accountID))
	return accountID + "." + hex.EncodeToString(mac.Sum(nil))
}

func (m *AccountManager) verifySessionCookie(value string) (string, bool) {
	accountID, signature, found := strings.Cut(value, ".")
	if !found {
		return "", false
	}
	mac := hmac.New(sha256.New, m.sessionKey)
	mac.Write([]byte(accountID))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return "", false
	}
	return accountID, true
}

func (m *AccountManager) setSessionCookie(w http.ResponseWriter, accountID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    m.sessionCookieValue(accountID),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 30,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
}

// requireAuth redirects anyone without a valid session to the login page,
// otherwise attaches the authenticated account ID to the request context.
func (m *AccountManager) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		accountID, ok := m.verifySessionCookie(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, withAccountID(r, accountID))
	})
}

type contextKey int

const accountIDContextKey contextKey = 0

func withAccountID(r *http.Request, accountID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), accountIDContextKey, accountID))
}

func accountIDFromRequest(r *http.Request) string {
	id, _ := r.Context().Value(accountIDContextKey).(string)
	return id
}

type authPageData struct {
	View  string
	Error string
}

func (s *Server) signupPage(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "signup", authPageData{View: "signup"})
}

func (s *Server) signupSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "signup", authPageData{View: "signup", Error: "Invalid input"})
		return
	}
	if r.FormValue("password") != r.FormValue("confirm") {
		s.render(w, "signup", authPageData{View: "signup", Error: "Passwords do not match"})
		return
	}
	account, err := s.accounts.signUp(r.FormValue("username"), r.FormValue("password"), r.FormValue("invite_code"))
	if err != nil {
		message := err.Error()
		if errors.Is(err, ErrInviteCodeRequired) {
			message = "Invalid invite code"
		}
		s.render(w, "signup", authPageData{View: "signup", Error: message})
		return
	}
	s.accounts.setSessionCookie(w, account.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "login", authPageData{View: "login"})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "login", authPageData{View: "login", Error: "Invalid input"})
		return
	}
	account, err := s.accounts.authenticate(r.FormValue("username"), r.FormValue("password"))
	if err != nil {
		s.render(w, "login", authPageData{View: "login", Error: "Invalid username or password"})
		return
	}
	s.accounts.setSessionCookie(w, account.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
