package events

import (
	"fmt"
	"testing"
	"time"
)

func ev(key string, t time.Time) Event { return Event{Key: key, Time: t} }

func TestStoreDedupeAndOrder(t *testing.T) {
	s := NewStore(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	added := s.Add(ev("a", base), ev("b", base.Add(2*time.Minute)), ev("c", base.Add(time.Minute)), ev("a", base.Add(time.Hour)))
	if len(added) != 3 {
		t.Fatalf("added %d, want 3", len(added))
	}
	got := s.List()
	want := []string{"b", "c", "a"}
	for i, k := range want {
		if got[i].Key != k {
			t.Fatalf("order = %v, want %v", keys(got), want)
		}
	}
	if added := s.Add(ev("b", base)); len(added) != 0 {
		t.Fatalf("duplicate was added")
	}
}

func TestStoreCapacity(t *testing.T) {
	s := NewStore(DefaultCapacity)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 150 {
		s.Add(ev(fmt.Sprint(i), base.Add(time.Duration(i)*time.Second)))
	}
	if s.Len() != DefaultCapacity {
		t.Fatalf("len = %d, want %d", s.Len(), DefaultCapacity)
	}
	l := s.List()
	if l[0].Key != "149" || l[len(l)-1].Key != "50" {
		t.Fatalf("kept %s..%s, want 149..50", l[0].Key, l[len(l)-1].Key)
	}
	// Older than everything in a full store: dropped, not reported as added.
	if added := s.Add(ev("old", base)); len(added) != 0 {
		t.Fatalf("old event reported as added")
	}
	// An evicted key may come back once it is newer than the tail.
	if added := s.Add(ev("new", base.Add(time.Hour))); len(added) != 1 || s.Len() != DefaultCapacity {
		t.Fatalf("new event not added correctly")
	}
}

func keys(es []Event) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Key
	}
	return out
}
