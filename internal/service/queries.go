package service

import (
	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
)

func (s *Service) ChamberSnapshot() model.ChamberSnapshot { return s.Chamber.Snapshot() }

// Events returns the most recent limit journal events in chronological order.
func (s *Service) Events(limit int) []model.Event {
	values, err := s.Journal.Events(limit)
	if err != nil {
		return []model.Event{}
	}
	return values
}

// EventsPage returns a stable cursor-based page of journal events. before is
// the opaque NextBefore cursor from a previous page; pass -1 for the newest
// page. Appends between calls never shift or drop records already paged.
func (s *Service) EventsPage(limit int, before int64) journal.EventPage {
	page, err := s.Journal.EventsPage(limit, before)
	if err != nil {
		return journal.EventPage{Events: []model.Event{}, NextBefore: -1}
	}
	return page
}
