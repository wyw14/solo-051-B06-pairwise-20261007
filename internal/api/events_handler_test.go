package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
)

func newEventService(t *testing.T) *service.Service {
	t.Helper()
	t.Setenv("OPTICCOAT_DATA", t.TempDir())
	svc := service.New()
	for i := 0; i < 3; i++ {
		if err := svc.Journal.Append(model.NewEvent("incident", model.NewID(), map[string]any{"n": float64(i)})); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

func TestEventsEndpointReturnsNewestPage(t *testing.T) {
	svc := newEventService(t)
	handler := listEvents(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/events?limit=2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var page journal.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || !page.HasMore || page.NextBefore < 0 {
		t.Fatalf("page = %+v", page)
	}
	if got := page.Events[1].Data["n"]; got != float64(2) {
		t.Fatalf("newest event n = %v, want 2", got)
	}

	// Follow the cursor to the final (oldest) page.
	req = httptest.NewRequest(http.MethodGet, "/api/events?limit=2&before="+strconv.FormatInt(page.NextBefore, 10), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.HasMore || page.NextBefore != -1 {
		t.Fatalf("oldest page = %+v", page)
	}
	if got := page.Events[0].Data["n"]; got != float64(0) {
		t.Fatalf("oldest event n = %v, want 0", got)
	}
}

func TestEventsEndpointRejectsBadParams(t *testing.T) {
	svc := newEventService(t)
	handler := listEvents(svc)
	for _, target := range []string{"/api/events?limit=-1", "/api/events?before=abc"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, rec.Code)
		}
	}
}
