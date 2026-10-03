package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SessionCookieName is the HttpOnly cookie used for browser sessions.
const SessionCookieName = "dnszm_session"

const rememberFor = 30 * 24 * time.Hour

// SessionStore holds hashed browser session tokens in process memory.
// Restarting the process invalidates cookies; that is intentional for a
// first-party cookie (the user signs in again). Do not put the API key in
// Web Storage.
type SessionStore struct {
	mu   sync.Mutex
	byID map[string]storedSession
}

type storedSession struct {
	User    User
	Expires time.Time
}

// NewSessionStore returns an empty store.
func NewSessionStore() *SessionStore {
	return &SessionStore{byID: map[string]storedSession{}}
}

var defaultSessions = NewSessionStore()

// Issue creates a new session for user and returns the raw cookie value.
func (s *SessionStore) Issue(user User, remember bool) (string, time.Duration, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	token := hex.EncodeToString(raw)
	ttl := time.Duration(0)
	exp := time.Now().Add(24 * time.Hour) // bound even session cookies server-side
	if remember {
		ttl = rememberFor
		exp = time.Now().Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(time.Now())
	s.byID[hashToken(token)] = storedSession{User: user, Expires: exp}
	return token, ttl, nil
}

// Lookup returns the user for a raw cookie token.
func (s *SessionStore) Lookup(token string) (User, bool) {
	if s == nil || token == "" {
		return User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.gcLocked(now)
	st, ok := s.byID[hashToken(token)]
	if !ok || now.After(st.Expires) {
		return User{}, false
	}
	return st.User, true
}

// Revoke deletes a raw cookie token.
func (s *SessionStore) Revoke(token string) {
	if s == nil || token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, hashToken(token))
}

func (s *SessionStore) gcLocked(now time.Time) {
	for id, st := range s.byID {
		if now.After(st.Expires) {
			delete(s.byID, id)
		}
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// WriteSessionCookie sets the HttpOnly session cookie.
func WriteSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge time.Duration) {
	c := &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieSecure(r),
	}
	if maxAge > 0 {
		c.MaxAge = int(maxAge.Seconds())
		c.Expires = time.Now().Add(maxAge)
	}
	http.SetCookie(w, c)
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieSecure(r),
		MaxAge:   -1,
	})
}

func cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
