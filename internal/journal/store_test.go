package journal

import (
	"fmt"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
)

func appendEvents(t *testing.T, store *Store, prefix string, count int) []model.Event {
	t.Helper()
	appended := make([]model.Event, 0, count)
	for i := 0; i < count; i++ {
		event := model.NewEvent(fmt.Sprintf("%s.%d", prefix, i), model.NewID(), nil)
		if err := store.Append(event); err != nil {
			t.Fatalf("append %s.%d: %v", prefix, i, err)
		}
		appended = append(appended, event)
	}
	return appended
}

func eventKinds(events []model.Event) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func TestEventsLimitReturnsLatestPage(t *testing.T) {
	store := New(t.TempDir())
	appended := appendEvents(t, store, "event", 10)

	latest, err := store.Events(3)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(latest) != 3 {
		t.Fatalf("expected 3 events, got %d", len(latest))
	}
	for i, event := range latest {
		want := appended[len(appended)-3+i]
		if event.ID != want.ID || event.Kind != want.Kind {
			t.Fatalf("position %d: expected %s (%s), got %s (%s)", i, want.Kind, want.ID, event.Kind, event.ID)
		}
	}
}

func TestEventsLimitBoundaries(t *testing.T) {
	store := New(t.TempDir())
	appended := appendEvents(t, store, "event", 5)

	all, err := store.Events(0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(all) != len(appended) {
		t.Fatalf("limit=0: expected all %d events, got %d", len(appended), len(all))
	}

	over, err := store.Events(100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(over) != len(appended) {
		t.Fatalf("limit>total: expected all %d events, got %d", len(appended), len(over))
	}

	exact, err := store.Events(len(appended))
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(exact) != len(appended) || exact[0].ID != appended[0].ID {
		t.Fatalf("limit=total: expected full log, got %v", eventKinds(exact))
	}
}

func TestEventsLatestPageStableAfterAppends(t *testing.T) {
	store := New(t.TempDir())
	appendEvents(t, store, "event", 8)

	before, err := store.Events(3)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	again, err := store.Events(3)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if fmt.Sprint(eventKinds(before)) != fmt.Sprint(eventKinds(again)) {
		t.Fatalf("page changed without appends: %v vs %v", eventKinds(before), eventKinds(again))
	}

	failures := appendEvents(t, store, "failure", 2)
	after, err := store.Events(3)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(after) != 3 {
		t.Fatalf("expected 3 events, got %d", len(after))
	}
	if after[1].ID != failures[0].ID || after[2].ID != failures[1].ID {
		t.Fatalf("tail failures missing from latest page: %v", eventKinds(after))
	}
	if after[0].ID != before[2].ID {
		t.Fatalf("window did not shift by appended count: %v", eventKinds(after))
	}
}

func TestEventsEmptyLog(t *testing.T) {
	store := New(t.TempDir())
	events, err := store.Events(10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if events == nil || len(events) != 0 {
		t.Fatalf("expected empty non-nil slice, got %v", events)
	}
}
