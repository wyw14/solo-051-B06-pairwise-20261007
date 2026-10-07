package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
)

func eventWithKind(kind string) model.Event {
	return model.Event{ID: model.NewID(), Kind: kind, RunID: model.NewID(), Data: map[string]any{}}
}

func kinds(events []model.Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Kind)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func marshalLine(t *testing.T, event model.Event) []byte {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestEventsReturnsMostRecentNotHead(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	for i := 0; i < 5; i++ {
		if err := store.Append(eventWithKind(fmt.Sprintf("e%d", i))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := store.Events(3)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	// Must be the tail in chronological order, not the head of the file.
	if want := []string{"e2", "e3", "e4"}; !equalStrings(kinds(got), want) {
		t.Fatalf("Events(3) = %v, want %v", kinds(got), want)
	}
}

func TestEventsLimitZeroReturnsAll(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	for i := 0; i < 3; i++ {
		_ = store.Append(eventWithKind(fmt.Sprintf("e%d", i)))
	}
	got, err := store.Events(0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if want := []string{"e0", "e1", "e2"}; !equalStrings(kinds(got), want) {
		t.Fatalf("Events(0) = %v, want %v", kinds(got), want)
	}
}

func TestEventsPageWalksAllWithoutGapsOrDuplicates(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	// Enough records to span several 4096-byte backward-scan blocks.
	const total = 200
	for i := 0; i < total; i++ {
		_ = store.Append(eventWithKind(fmt.Sprintf("e%03d", i)))
	}

	// Pages walk newest-to-oldest; within each page events ascend.
	var flat []string
	before := int64(-1)
	pageCount := 0
	for {
		page, err := store.EventsPage(25, before)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		if len(page.Events) == 0 {
			t.Fatalf("empty page before exhaustion, got %d records", len(flat))
		}
		pageKinds := kinds(page.Events)
		for i := 1; i < len(pageKinds); i++ {
			if pageKinds[i-1] >= pageKinds[i] {
				t.Fatalf("page %d not ascending: %s after %s", pageCount, pageKinds[i], pageKinds[i-1])
			}
		}
		flat = append(flat, pageKinds...)
		pageCount++
		if !page.HasMore {
			break
		}
		if page.NextBefore < 0 {
			t.Fatalf("has_more but missing cursor")
		}
		before = page.NextBefore
	}
	if len(flat) != total {
		t.Fatalf("paged %d records, want %d", len(flat), total)
	}
	// Block 0 is the newest page (e175..e199), the last block the oldest
	// (e000..e024); verify every position with no gap or duplicate.
	for block := 0; block < pageCount; block++ {
		base := total - (block+1)*25
		size := 25
		if base < 0 {
			base = 0
			size = total % 25
		}
		for j := 0; j < size; j++ {
			want := fmt.Sprintf("e%03d", base+j)
			if got := flat[block*25+j]; got != want {
				t.Fatalf("flat[%d] = %s, want %s", block*25+j, got, want)
			}
		}
	}
	if pageCount != 8 {
		t.Fatalf("pages = %d, want 8", pageCount)
	}
}

func TestCursorStableAcrossAppends(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	for i := 0; i < 10; i++ {
		_ = store.Append(eventWithKind(fmt.Sprintf("e%02d", i)))
	}
	latest, err := store.EventsPage(4, -1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e06", "e07", "e08", "e09"}; !equalStrings(kinds(latest.Events), want) {
		t.Fatalf("latest page = %v, want %v", kinds(latest.Events), want)
	}
	if !latest.HasMore || latest.NextBefore < 0 {
		t.Fatalf("expected more pages, got %+v", latest)
	}

	// Appends issued after the cursor must not shift or invalidate old pages.
	for i := 10; i < 15; i++ {
		_ = store.Append(eventWithKind(fmt.Sprintf("e%02d", i)))
	}
	older, err := store.EventsPage(4, latest.NextBefore)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e02", "e03", "e04", "e05"}; !equalStrings(kinds(older.Events), want) {
		t.Fatalf("older page after appends = %v, want %v", kinds(older.Events), want)
	}

	// A fresh newest page must reach the newly appended tail.
	fresh, err := store.EventsPage(4, -1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e11", "e12", "e13", "e14"}; !equalStrings(kinds(fresh.Events), want) {
		t.Fatalf("fresh latest = %v, want %v", kinds(fresh.Events), want)
	}
}

func TestTornTailDoesNotHideLatestFailure(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Append(eventWithKind("run.started")); err != nil {
		t.Fatal(err)
	}

	// Crash mid-append: only half the bytes of an incident line land, with no
	// terminating newline. The partial tail must be ignored, not surfaced.
	path := filepath.Join(dir, "events.jsonl")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	torn := marshalLine(t, eventWithKind("incident.lost"))
	if err := os.WriteFile(path, append(existing, torn[:len(torn)/2]...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := store.Events(10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if want := []string{"run.started"}; !equalStrings(kinds(got), want) {
		t.Fatalf("with torn tail = %v, want complete records only %v", kinds(got), want)
	}

	// Recovery truncates back to the last newline, then the real tail failure
	// is appended: it must be visible as the newest event.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cut := len(data)
	for cut > 0 && data[cut-1] != '\n' {
		cut--
	}
	if err := os.WriteFile(path, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(eventWithKind("incident.pump_failure")); err != nil {
		t.Fatal(err)
	}
	got, err = store.Events(10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if last := got[len(got)-1]; last.Kind != "incident.pump_failure" {
		t.Fatalf("latest event = %q, want incident.pump_failure", last.Kind)
	}

	// The newest single-record page reaches the failure too.
	page, err := store.EventsPage(1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Kind != "incident.pump_failure" {
		t.Fatalf("newest page = %v, want only the pump failure", kinds(page.Events))
	}
}

func TestEmptyAndMissingStore(t *testing.T) {
	store := New(t.TempDir())
	page, err := store.EventsPage(10, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.HasMore || page.NextBefore != -1 {
		t.Fatalf("empty page = %+v", page)
	}
	events, err := store.Events(10)
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %v, %v", events, err)
	}
}

func TestEventsAcrossBlockBoundaries(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	small := eventWithKind("small")
	big := model.Event{ID: model.NewID(), Kind: "big", RunID: model.NewID(), Data: map[string]any{"pad": strings.Repeat("x", 12000)}}
	tail := eventWithKind("tail")
	for _, event := range []model.Event{small, big, tail} {
		if err := store.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"small", "big", "tail"}; !equalStrings(kinds(got), want) {
		t.Fatalf("events = %v, want %v", kinds(got), want)
	}
	if pad, _ := got[1].Data["pad"].(string); len(pad) != 12000 {
		t.Fatalf("big record payload = %d bytes, want 12000", len(pad))
	}

	// One-record newest page while the middle line spans several blocks.
	page, err := store.EventsPage(1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tail"}; !equalStrings(kinds(page.Events), want) || !page.HasMore {
		t.Fatalf("newest page = %+v", page)
	}
}

func TestLastPageHasNoCursor(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	for i := 0; i < 5; i++ {
		_ = store.Append(eventWithKind(fmt.Sprintf("e%d", i)))
	}
	page, err := store.EventsPage(10, -1) // whole journal in one page
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e0", "e1", "e2", "e3", "e4"}; !equalStrings(kinds(page.Events), want) {
		t.Fatalf("page = %v, want %v", kinds(page.Events), want)
	}
	if page.HasMore {
		t.Fatalf("single page must report has_more=false")
	}
	if page.NextBefore != -1 {
		t.Fatalf("next_before = %d, want -1 at the head", page.NextBefore)
	}
}
