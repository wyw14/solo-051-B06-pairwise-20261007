package journal

import (
	"bytes"
	"encoding/json"
	"github.com/wyw14/cry-161/internal/model"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu  sync.Mutex
	dir string
}

func New(dir string) *Store { _ = os.MkdirAll(dir, 0o755); return &Store{dir: dir} }

func (s *Store) path() string { return filepath.Join(s.dir, "events.jsonl") }

func (s *Store) Append(event model.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// EventPage is one stable slice of the append-only journal. Events are always
// returned in chronological (file) order. NextBefore is the byte offset of the
// oldest returned record and is the cursor for the next older page; it is -1
// when the page is empty or no older records remain.
type EventPage struct {
	Events     []model.Event `json:"events"`
	NextBefore int64         `json:"next_before"`
	HasMore    bool          `json:"has_more"`
}

// Events returns the most recent limit records in chronological order; a
// limit <= 0 returns every complete record. The audit/operations surface
// treats the journal as recent-event history, so the tail of the file is what
// matters: reading the head hid incidents appended at the end.
func (s *Store) Events(limit int) ([]model.Event, error) {
	page, err := s.EventsPage(limit, -1)
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

// EventsPage returns at most limit complete records that start strictly before
// the byte offset before (-1 means the current end of file), scanning the file
// backwards and restoring the matches to chronological order.
//
// The journal is append-only, so byte offsets of records already written never
// shift: a cursor issued before later appends keeps returning the same page
// instead of drifting or dropping the tail. A record counts as complete only
// once its terminating '\n' is on disk; a torn tail from an interrupted append
// (or a corrupt line) is skipped rather than parsed or mistaken for the newest
// event.
func (s *Store) EventsPage(limit int, before int64) (EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	page := EventPage{Events: []model.Event{}, NextBefore: -1}
	f, err := os.Open(s.path())
	if os.IsNotExist(err) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	defer f.Close()

	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return page, err
	}
	window := end
	if before >= 0 && before < end {
		window = before
	}
	if window <= 0 {
		return page, nil
	}

	// Fetch one extra oldest record purely as a HasMore probe.
	want := limit
	if want <= 0 {
		want = int(^uint(0) >> 1)
	}
	capacity := want
	if limit > 0 {
		capacity++
	}

	// Backward block scan. Until the first real newline is found, `edge` is
	// true and the segment beyond the window edge must not be emitted: that
	// hides bytes after the file's last newline (a torn tail) and the
	// zero-length segment starting at a cursor. `carry` holds line bytes
	// gathered from blocks further to the right.
	slots := make([]slot, 0, min(capacity, 64))
	var (
		edge  = true
		pos   = window
		carry []byte
	)
	const blockSize = 4096
finished:
	for pos > 0 {
		size := int64(blockSize)
		if size > pos {
			size = pos
		}
		pos -= size
		base := pos
		chunk := make([]byte, size)
		if _, err := f.ReadAt(chunk, base); err != nil && err != io.EOF {
			return page, err
		}
		hi := len(chunk)
		for {
			j := bytes.LastIndexByte(chunk[:hi], '\n')
			if j < 0 {
				// This remainder continues the line terminated by the
				// newline already found in a block further right.
				carry = append(append(make([]byte, 0, hi+len(carry)), chunk[:hi]...), carry...)
				break
			}
			if !edge {
				// The newline at base+j terminates the record running to the
				// newline found previously; that record starts just after it.
				line := chunk[j+1 : hi]
				if len(carry) > 0 {
					line = append(append(make([]byte, 0, len(line)+len(carry)), line...), carry...)
				}
				if event, ok := decode(line); ok {
					slots = append(slots, slot{offset: base + int64(j) + 1, event: event})
					if len(slots) >= capacity {
						break finished
					}
				}
			}
			edge = false
			carry = nil
			hi = j
		}
		if base == 0 {
			// Whatever is left in carry is the first line of the file.
			if !edge {
				if event, ok := decode(carry); ok {
					slots = append(slots, slot{offset: 0, event: event})
				}
			}
			break
		}
	}

	if len(slots) == 0 {
		return page, nil
	}
	if limit > 0 && len(slots) > want {
		// The oldest slot was only a probe proving more records exist.
		page.HasMore = true
		slots = slots[:want]
	}
	// Slots were gathered newest-first, so the oldest kept is the last one;
	// its start offset is the cursor for the preceding page.
	oldest := slots[len(slots)-1]
	if oldest.offset > 0 {
		page.NextBefore = oldest.offset
	}
	for i := len(slots)/2 - 1; i >= 0; i-- {
		opp := len(slots) - 1 - i
		slots[i], slots[opp] = slots[opp], slots[i]
	}
	page.Events = make([]model.Event, len(slots))
	for i, item := range slots {
		page.Events[i] = item.event
	}
	return page, nil
}

type slot struct {
	offset int64
	event  model.Event
}

func decode(line []byte) (model.Event, bool) {
	var event model.Event
	if len(line) == 0 {
		return event, false
	}
	if err := json.Unmarshal(line, &event); err != nil {
		// Skip a corrupt or partial (not newline-terminated) record but keep
		// scanning older ones.
		return event, false
	}
	return event, true
}
