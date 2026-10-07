package api

import (
	"net/http"
	"strconv"

	"github.com/wyw14/cry-161/internal/service"
)

// listEvents serves the most recent journal events with stable cursor paging:
//
//	GET /api/events?limit=50&before=<next_before from previous page>
//
// Pages walk from newest to oldest while every page's events stay in
// chronological order. The cursor is a byte offset into the append-only
// journal, so records appended after a page is fetched never invalidate it.
func listEvents(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			limit = parsed
		}
		before := int64(-1)
		if raw := r.URL.Query().Get("before"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				http.Error(w, "invalid before cursor", http.StatusBadRequest)
				return
			}
			before = parsed
		}
		writeJSON(w, svc.EventsPage(limit, before))
	}
}
