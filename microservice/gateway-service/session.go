package main

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// sessionCookie is the name of the cookie carrying the session
// token. It is HttpOnly, so page scripts cannot read it and an
// injected script cannot steal it.
const sessionCookie = "session"

// sessionLifetime is how long a sign-in lasts before the
// customer has to authenticate again.
const sessionLifetime = 12 * time.Hour

// session records who is signed in and until when.
type session struct {
	customerID string
	expiresAt  time.Time
}

// SessionStore keeps sign-ins in memory, keyed by an opaque
// random token. The token is the only thing the browser ever
// holds; the customer ID stays server-side, so a shopper
// cannot change who they are by editing a cookie.
//
// In-memory means sessions are lost when the gateway restarts,
// which is acceptable for this lab. A production deployment
// would keep them in a shared store so any instance of the
// gateway could serve any request.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]session)}
}

// newToken returns a cryptographically random, URL-safe token.
func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Create starts a session for a customer and returns its
// token.
func (s *SessionStore) Create(customerID string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Opportunistically drop anything that has already expired,
	// so a long-running gateway does not accumulate dead
	// sessions.
	now := time.Now()
	for key, existing := range s.sessions {
		if now.After(existing.expiresAt) {
			delete(s.sessions, key)
		}
	}

	s.sessions[token] = session{
		customerID: customerID,
		expiresAt:  now.Add(sessionLifetime),
	}

	return token, nil
}

// Lookup returns the customer ID for a token, and whether the
// token names a session that is still valid.
func (s *SessionStore) Lookup(token string) (string, bool) {
	if token == "" {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	found, exists := s.sessions[token]
	if !exists {
		return "", false
	}
	if time.Now().After(found.expiresAt) {
		delete(s.sessions, token)
		return "", false
	}

	return found.customerID, true
}

// Destroy ends a session, if it exists.
func (s *SessionStore) Destroy(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// ---------- cookie plumbing ----------

// setSessionCookie writes the session cookie on a successful
// sign-in.
//
// The cookie is not marked Secure because this lab runs over
// plain HTTP on localhost; over HTTPS it should be, and
// SameSite=Lax already blocks it from being sent on
// cross-site form posts.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
}

// clearSessionCookie expires the cookie on sign-out.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// tokenFrom reads the session token off a request, returning
// "" when there is no session cookie.
func tokenFrom(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
