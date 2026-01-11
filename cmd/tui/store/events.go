package store

import "time"

type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarning
	LevelError
)

const (
	HistoryLimit     = 500
	DefaultStatusTTL = 5 * time.Second
)

func (level Level) String() string {
	switch level {
	case LevelSuccess:
		return "OK"
	case LevelWarning:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Sticky reports whether events of this level stay on the status line until replaced or cleared.
func (level Level) Sticky() bool {
	return level >= LevelWarning
}

// Event is one status message. A zero ID means no event.
type Event struct {
	ID    uint64
	Level Level
	Text  string
	Time  time.Time
}

// Notify records an event in the history, shows it on the status line and returns its ID.
// Info and success events expire from the status line after the status TTL.
func (store *Store) Notify(level Level, text string) uint64 {
	var id uint64
	store.setState(func(state *State) bool {
		store.lastEventID++
		id = store.lastEventID
		event := Event{ID: id, Level: level, Text: text, Time: store.now()}
		state.Status = event
		state.History = appendEvent(state.History, event)
		if level.Sticky() {
			state.Unread++
		}
		return true
	})
	if !level.Sticky() && store.statusTTL > 0 {
		store.afterFunc(store.statusTTL, func() { store.ExpireStatus(id) })
	}
	return id
}

// ExpireStatus clears the status line only when it still shows the event with the given ID.
func (store *Store) ExpireStatus(id uint64) {
	store.setState(func(state *State) bool {
		if id == 0 || state.Status.ID != id {
			return false
		}
		state.Status = Event{}
		return true
	})
}

func (store *Store) ClearStatus() {
	store.setState(func(state *State) bool {
		if state.Status.ID == 0 {
			return false
		}
		state.Status = Event{}
		return true
	})
}

// MarkHistoryRead acknowledges the events up to and including throughID. Warnings and errors recorded after it,
// such as one that arrives while the history dialog opens, stay unread.
func (store *Store) MarkHistoryRead(throughID uint64) {
	store.setState(func(state *State) bool {
		unread := 0
		for _, event := range state.History {
			if event.ID > throughID && event.Level.Sticky() {
				unread++
			}
		}
		if unread == state.Unread {
			return false
		}
		state.Unread = unread
		return true
	})
}

func appendEvent(history []Event, event Event) []Event {
	start := 0
	if len(history) >= HistoryLimit {
		start = len(history) - HistoryLimit + 1
	}
	next := make([]Event, 0, len(history)-start+1)
	next = append(next, history[start:]...)
	return append(next, event)
}
