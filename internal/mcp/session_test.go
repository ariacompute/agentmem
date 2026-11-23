package mcp

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionsCreateTouchDelete(t *testing.T) {
	s := NewSessions(time.Hour, 2)
	first, err := s.Create(Version20250618)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Version != Version20250618 {
		t.Fatalf("session = %+v", first)
	}
	touched, ok := s.Touch(first.ID)
	if !ok || touched.ID != first.ID {
		t.Fatalf("touch = %+v, %v", touched, ok)
	}
	if _, ok := s.Touch(""); ok {
		t.Fatal("empty id must not resolve")
	}
	if _, ok := s.Touch("unknown"); ok {
		t.Fatal("unknown id must not resolve")
	}
	if !s.Delete(first.ID) {
		t.Fatal("delete must report the removal")
	}
	if s.Delete(first.ID) {
		t.Fatal("double delete must report false")
	}
}

func TestSessionsCapacity(t *testing.T) {
	s := NewSessions(time.Hour, 1)
	if _, err := s.Create(Version20250326); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Version20250326); !errors.Is(err, ErrSessionCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	if s.Len() != 1 {
		t.Fatalf("len = %d", s.Len())
	}
}

func TestSessionsExpireOnTouch(t *testing.T) {
	s := NewSessions(time.Minute, 4)
	now := time.Now()
	s.now = func() time.Time { return now }
	s.lastSweep = now
	created, err := s.Create(Version20250618)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := s.Touch(created.ID); ok {
		t.Fatal("expired session must not resolve")
	}
	if s.Len() != 0 {
		t.Fatalf("expired session still counted: %d", s.Len())
	}
}

func TestSessionsDefaultsAndConcurrency(t *testing.T) {
	s := NewSessions(0, 0)
	if s.ttl != 30*time.Minute || s.max != 128 {
		t.Fatalf("defaults = %v/%d", s.ttl, s.max)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := s.Create(Version20250326)
			if err != nil {
				t.Error(err)
				return
			}
			if _, ok := s.Touch(session.ID); !ok {
				t.Error("session vanished")
			}
		}()
	}
	wg.Wait()
	if s.Len() != 8 {
		t.Fatalf("len = %d", s.Len())
	}
}
