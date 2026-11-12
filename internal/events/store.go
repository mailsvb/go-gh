package events

import (
	"slices"
	"sync"
)

// DefaultCapacity is the number of events kept in memory.
const DefaultCapacity = 100

// Store keeps the newest events in memory only. Nothing is written to disk;
// after a restart the store is rebuilt from the GitHub Events API.
type Store struct {
	mu     sync.RWMutex
	cap    int
	events []Event         // sorted newest first
	keys   map[string]bool // keys present in events
}

// NewStore returns a store holding at most capacity events.
func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{cap: capacity, keys: make(map[string]bool)}
}

// Add inserts events and reports which of them were new. Duplicates (same
// Key) and events older than the oldest retained event in a full store are
// dropped.
func (s *Store) Add(evts ...Event) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	var added []Event
	for _, e := range evts {
		if e.Key == "" || s.keys[e.Key] {
			continue
		}
		if len(s.events) >= s.cap && !e.Time.After(s.events[len(s.events)-1].Time) {
			continue
		}
		i, _ := slices.BinarySearchFunc(s.events, e, func(a, b Event) int {
			// newest first
			return b.Time.Compare(a.Time)
		})
		s.events = slices.Insert(s.events, i, e)
		s.keys[e.Key] = true
		added = append(added, e)
		if len(s.events) > s.cap {
			drop := s.events[len(s.events)-1]
			s.events = s.events[:len(s.events)-1]
			delete(s.keys, drop.Key)
			if drop.Key == e.Key {
				added = added[:len(added)-1]
			}
		}
	}
	return added
}

// List returns a copy of the events, newest first.
func (s *Store) List() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.events)
}

// Len returns the number of events stored.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// Clear removes all events.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
	s.keys = make(map[string]bool)
}
