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
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Account is a teacher's login identity. Their planner data lives in a
// separate per-account Store, so every teacher's schedule, agendas, notes,
// and settings are completely isolated from one another.
type Account struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	LastLoginAt  time.Time `json:"lastLoginAt,omitempty"`
	// SessionVersion is embedded in session cookies; bumping it (e.g. on an
	// admin password reset) invalidates every session already issued.
	SessionVersion int `json:"sessionVersion"`
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
	// adminUsernames lists the (lowercased) usernames allowed into /admin,
	// configured via the ADMIN_USERNAMES env var.
	adminUsernames map[string]bool
	// lastSeen tracks recent activity per account ID, in memory only, to
	// approximate whether someone is currently logged on.
	lastSeen map[string]time.Time
}

func newAccountManager(baseDir string) *AccountManager {
	m := &AccountManager{
		stores:         make(map[string]*Store),
		inviteCode:     os.Getenv("SIGNUP_INVITE_CODE"),
		adminUsernames: parseAdminUsernames(os.Getenv("ADMIN_USERNAMES")),
		lastSeen:       make(map[string]time.Time),
	}
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

func parseAdminUsernames(raw string) map[string]bool {
	admins := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			admins[name] = true
		}
	}
	return admins
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
	account := Account{ID: hex.EncodeToString(randomBytes(8)), Username: username, PasswordHash: string(hash), CreatedAt: time.Now()}
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

// accountByID returns a copy of the account with the given ID.
func (m *AccountManager) accountByID(id string) (Account, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, account := range m.accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

// listAccounts returns a copy of every registered account.
func (m *AccountManager) listAccounts() []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Account(nil), m.accounts...)
}

// isAdminUsername reports whether username is listed in ADMIN_USERNAMES.
func (m *AccountManager) isAdminUsername(username string) bool {
	return m.adminUsernames[strings.ToLower(strings.TrimSpace(username))]
}

// touchLogin records that an account just logged in successfully.
func (m *AccountManager) touchLogin(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.accounts[i].ID == id {
			m.accounts[i].LastLoginAt = time.Now()
			_ = m.persistAccountsLocked()
			return
		}
	}
}

// touchSeen records recent activity for the "currently logged on" indicator.
func (m *AccountManager) touchSeen(id string) {
	m.mu.Lock()
	m.lastSeen[id] = time.Now()
	m.mu.Unlock()
}

func (m *AccountManager) lastSeenAt(id string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen, ok := m.lastSeen[id]
	return seen, ok
}

// deleteAccount removes an account, its planner data, and its cached store.
func (m *AccountManager) deleteAccount(id string) error {
	m.mu.Lock()
	index := -1
	for i, account := range m.accounts {
		if account.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.accounts = append(m.accounts[:index:index], m.accounts[index+1:]...)
	err := m.persistAccountsLocked()
	delete(m.stores, id)
	delete(m.lastSeen, id)
	usersDir := m.usersDir
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if usersDir != "" {
		_ = os.Remove(filepath.Join(usersDir, id+".json"))
	}
	return nil
}

// resetPassword sets a new password for an account and bumps its session
// version, which signs that account out of every device immediately.
func (m *AccountManager) resetPassword(id, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.accounts[i].ID == id {
			m.accounts[i].PasswordHash = string(hash)
			m.accounts[i].SessionVersion++
			return m.persistAccountsLocked()
		}
	}
	return errors.New("account not found")
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

func (m *AccountManager) sessionCookieValue(accountID string, version int) string {
	payload := accountID + ":" + strconv.Itoa(version)
	mac := hmac.New(sha256.New, m.sessionKey)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (m *AccountManager) verifySessionCookie(value string) (accountID string, version int, ok bool) {
	payload, signature, found := strings.Cut(value, ".")
	if !found {
		return "", 0, false
	}
	accountID, versionRaw, found := strings.Cut(payload, ":")
	if !found {
		return "", 0, false
	}
	version, err := strconv.Atoi(versionRaw)
	if err != nil {
		return "", 0, false
	}
	mac := hmac.New(sha256.New, m.sessionKey)
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return "", 0, false
	}
	return accountID, version, true
}

func (m *AccountManager) setSessionCookie(w http.ResponseWriter, accountID string, version int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    m.sessionCookieValue(accountID, version),
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
		accountID, version, ok := m.verifySessionCookie(cookie.Value)
		if !ok {
			clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		account, found := m.accountByID(accountID)
		if !found || account.SessionVersion != version {
			// Account deleted or password was reset elsewhere: force re-login.
			clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		m.touchSeen(accountID)
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
	s.accounts.setSessionCookie(w, account.ID, account.SessionVersion)
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
	s.accounts.touchLogin(account.ID)
	s.accounts.setSessionCookie(w, account.ID, account.SessionVersion)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
