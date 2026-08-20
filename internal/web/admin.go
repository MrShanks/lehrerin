package web

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// onlineWindow is how recently an account must have made a request to be
// shown as "online now" in the admin panel.
const onlineWindow = 5 * time.Minute

type adminAccountRow struct {
	ID          string
	Username    string
	CreatedAt   string
	LastLoginAt string
	Online      bool
	IsAdmin     bool
	IsSelf      bool
}

type adminLoginAttemptRow struct {
	Time     string
	Username string
	IP       string
	Success  bool
	Result   string
}

type adminPageData struct {
	View          string
	Accounts      []adminAccountRow
	LoginAttempts []adminLoginAttemptRow
	Notice        string
	Error         string
}

// isAdminRequest reports whether the authenticated caller is an admin.
func (s *Server) isAdminRequest(r *http.Request) bool {
	account, ok := s.accounts.accountByID(accountIDFromRequest(r))
	return ok && s.accounts.isAdminUsername(account.Username)
}

// requireAdmin rejects any request from a non-admin account. It must be
// mounted behind requireAuth so accountIDFromRequest is already populated.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdminRequest(r) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	s.renderAdmin(w, r, "", "")
}

func (s *Server) renderAdmin(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	selfID := accountIDFromRequest(r)
	accounts := s.accounts.listAccounts()
	sort.Slice(accounts, func(i, j int) bool {
		return strings.ToLower(accounts[i].Username) < strings.ToLower(accounts[j].Username)
	})
	now := time.Now()
	rows := make([]adminAccountRow, len(accounts))
	for i, account := range accounts {
		online := false
		if seen, ok := s.accounts.lastSeenAt(account.ID); ok && now.Sub(seen) <= onlineWindow {
			online = true
		}
		rows[i] = adminAccountRow{
			ID:          account.ID,
			Username:    account.Username,
			CreatedAt:   formatAdminTime(account.CreatedAt),
			LastLoginAt: formatAdminTime(account.LastLoginAt),
			Online:      online,
			IsAdmin:     s.accounts.isAdminUsername(account.Username),
			IsSelf:      account.ID == selfID,
		}
	}
	s.render(w, "admin", adminPageData{View: "admin", Accounts: rows, LoginAttempts: loginAttemptRows(s.accounts.recentLoginAttempts(100)), Notice: notice, Error: errMsg})
}

func loginAttemptRows(attempts []loginLogEntry) []adminLoginAttemptRow {
	rows := make([]adminLoginAttemptRow, len(attempts))
	for i, attempt := range attempts {
		result := "Success"
		switch {
		case attempt.Success:
			result = "Success"
		case attempt.Reason == "rate_limited":
			result = "Blocked (too many attempts)"
		default:
			result = "Failed (wrong username or password)"
		}
		rows[i] = adminLoginAttemptRow{
			Time:     attempt.Time.Format("Jan 2, 2006 15:04:05"),
			Username: attempt.Username,
			IP:       attempt.IP,
			Success:  attempt.Success,
			Result:   result,
		}
	}
	return rows
}

func formatAdminTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return t.Format("Jan 2, 2006 15:04")
}

func (s *Server) adminDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == accountIDFromRequest(r) {
		s.renderAdmin(w, r, "", "You cannot delete your own account from the admin panel")
		return
	}
	if err := s.accounts.deleteAccount(id); err != nil {
		s.renderAdmin(w, r, "", err.Error())
		return
	}
	s.renderAdmin(w, r, "Account deleted", "")
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderAdmin(w, r, "", "Invalid input")
		return
	}
	account, ok := s.accounts.accountByID(id)
	if !ok {
		s.renderAdmin(w, r, "", "Account not found")
		return
	}
	newPassword := strings.TrimSpace(r.FormValue("new_password"))
	generated := newPassword == ""
	if generated {
		newPassword = hex.EncodeToString(randomBytes(9))
	}
	if err := s.accounts.resetPassword(id, newPassword); err != nil {
		s.renderAdmin(w, r, "", err.Error())
		return
	}
	notice := fmt.Sprintf("Password for %s was reset. They have been signed out everywhere.", account.Username)
	if generated {
		notice = fmt.Sprintf("Password for %s was reset to %q (copy it now, it won't be shown again). They have been signed out everywhere.", account.Username, newPassword)
	}
	s.renderAdmin(w, r, notice, "")
}
