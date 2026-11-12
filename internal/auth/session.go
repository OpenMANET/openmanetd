package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Session holds the state for an authenticated user session.
type Session struct {
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastAccess time.Time
	done       chan struct{} // closed when the session leaves its store
	Token      string
	Username   string
}

// Done returns a channel that is closed when the session is removed from its
// store: on logout, on eviction, or when the store finds it expired. A
// session that has passed ExpiresAt but has not yet been removed is not
// closed; use WithSessionLifetime to also stop at expiry.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// WithSessionLifetime returns a copy of parent that is canceled when the
// session ends (Done), when it reaches ExpiresAt, or when the returned cancel
// function is called. Connections authenticated once, such as a WebSocket at
// upgrade, run under it so they do not outlive the session. Callers must call
// cancel to release the watcher goroutine.
func WithSessionLifetime(parent context.Context, sess *Session) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithDeadline(parent, sess.ExpiresAt)

	go func() {
		select {
		case <-sess.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}

// SessionStore is a thread-safe in-memory store for active sessions.
type SessionStore struct {
	sessions map[string]*Session
	maxAge   time.Duration
	maxSize  int
	mu       sync.RWMutex
}

// NewSessionStore creates a SessionStore with the given session lifetime and
// maximum number of concurrent sessions. When the store is full, the session
// with the oldest LastAccess time is evicted to make room.
func NewSessionStore(maxAge time.Duration, maxSize int) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session, maxSize),
		maxAge:   maxAge,
		maxSize:  maxSize,
	}
}

// Create generates a new session token for the given username, stores the
// session, and returns the token. If the store is at capacity, the oldest
// session (by LastAccess) is evicted first.
func (s *SessionStore) Create(username string) string {
	token := generateToken()
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.sessions) >= s.maxSize {
		s.evictOldestLocked()
	}

	s.sessions[token] = &Session{
		Token:      token,
		Username:   username,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.maxAge),
		LastAccess: now,
		done:       make(chan struct{}),
	}

	return token
}

// Validate looks up a token and returns the associated session if it exists
// and has not expired. LastAccess is updated on a successful validation.
func (s *SessionStore) Validate(token string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[token]
	if !ok {
		return nil, false
	}

	if time.Now().After(sess.ExpiresAt) {
		s.removeLocked(token)

		return nil, false
	}

	sess.LastAccess = time.Now()

	return sess, true
}

// Delete removes a session by token. It is used during logout.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeLocked(token)
}

// Cleanup removes all expired sessions. It is intended to be called
// periodically by StartCleanup.
func (s *SessionStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	var expired []string

	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			expired = append(expired, token)
		}
	}

	for _, token := range expired {
		s.removeLocked(token)
	}
}

// StartCleanup starts a background goroutine that calls Cleanup on the given
// interval until ctx is canceled.
func (s *SessionStore) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Cleanup()
			}
		}
	}()
}

// evictOldestLocked removes the session with the oldest LastAccess time.
// The caller must hold s.mu (write lock).
func (s *SessionStore) evictOldestLocked() {
	var (
		oldestToken string
		oldestTime  time.Time
	)

	for token, sess := range s.sessions {
		if oldestToken == "" || sess.LastAccess.Before(oldestTime) {
			oldestToken = token
			oldestTime = sess.LastAccess
		}
	}

	if oldestToken != "" {
		s.removeLocked(oldestToken)
	}
}

// removeLocked deletes the session for token, if any, and closes its Done
// channel. The caller must hold s.mu (write lock).
func (s *SessionStore) removeLocked(token string) {
	sess, ok := s.sessions[token]
	if !ok {
		return
	}

	delete(s.sessions, token)
	close(sess.done)
}

// generateToken returns a 64-character hex-encoded cryptographically random token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// rand.Read is documented to always succeed on supported platforms.
		panic("auth: failed to generate session token: " + err.Error())
	}

	return hex.EncodeToString(b)
}
