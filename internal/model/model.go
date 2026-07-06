package model

import "time"

const (
	StatusPlanned   = "planned"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
)

// LocalDatetime is the layout used for starts_at/ends_at throughout the app.
// Naive local time — the service runs in a fixed timezone (see AppTZ).
const LocalDatetime = "2006-01-02T15:04"

// Appointment is one scheduled visit. It is the source of truth in SQLite and
// the unit that gets exported to Home Assistant's calendar.
type Appointment struct {
	ID         int64
	Title      string
	Person     string
	Location   string
	StartsAt   string // LocalDatetime
	EndsAt     string // LocalDatetime, "" if none
	Status     string
	Note       string
	Raw        string
	HaUID      string
	HaSyncedAt string
	CreatedAt  string
	UpdatedAt  string
	DeletedAt  string
}

// Start parses StartsAt in loc. Callers that need to compare against "now"
// should pass the same location the scheduler uses.
func (a Appointment) Start(loc *time.Location) (time.Time, error) {
	return time.ParseInLocation(LocalDatetime, a.StartsAt, loc)
}
