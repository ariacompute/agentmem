package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrSessionCapacity reports that the session table is full. It is a fixed
// sentinel so the transport can answer 429 without echoing any detail.
var ErrSessionCapacity = errors.New("mcp: session capacity reached")

// Session is one Streamable HTTP session. The identifier is a random 128-bit
// value; it carries no subject, so a leaked header never reveals who owns it.
type Session struct {
	ID      string
	Version string
	Created time.Time
	Seen    time.Time
}

// Sessions is a bounded, in-memory session table. State is deliberately not
// persisted: a session is a transport detail, and a restart invalidates it.
type Sessions struct {
	mu        sync.Mutex
	ttl       time.Duration
	max       int
	items     map[string]Session
	lastSweep time.Time
	now       func() time.Time
	newID     func() (string, error)
}

// NewSessions builds a table. A non-positive ttl or max falls back to the
// documented defaults so a zero-value configuration cannot disable the bound.
func NewSessions(ttl time.Duration, max int) *Sessions {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if max <= 0 {
		max = 128
	}
	return &Sessions{
		ttl: ttl, max: max, items: make(map[string]Session, max),
		lastSweep: time.Now(), now: time.Now, newID: randomID,
	}
}

func randomID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// Create registers a session for a negotiated revision, sweeping expired
// entries first so a long-running process cannot grow without bound.
func (s *Sessions) Create(version string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	if len(s.items) >= s.max {
		return Session{}, ErrSessionCapacity
	}
	id, err := s.newID()
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: id, Version: version, Created: now, Seen: now}
	s.items[id] = session
	return session, nil
}

// Touch resolves a session and refreshes its idle timer. An unknown or expired
// identifier is indistinguishable from a missing one.
func (s *Sessions) Touch(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return Session{}, false
	}
	now := s.now()
	s.sweepLocked(now)
	session, ok := s.items[id]
	if !ok {
		return Session{}, false
	}
	session.Seen = now
	s.items[id] = session
	return session, true
}

// Delete removes a session and reports whether it existed.
func (s *Sessions) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false
	}
	delete(s.items, id)
	return true
}

// Len reports the number of live sessions.
func (s *Sessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	return len(s.items)
}

func (s *Sessions) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	for id, session := range s.items {
		if now.Sub(session.Seen) > s.ttl {
			delete(s.items, id)
		}
	}
}
