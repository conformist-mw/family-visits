// Package ics renders appointments as an RFC 5545 VCALENDAR feed for Home
// Assistant's Remote Calendar integration to poll. SQLite stays the source of
// truth; each fetch is a full snapshot, so add/reschedule/cancel need no push.
package ics

import (
	"fmt"
	"strings"
	"time"

	"visits/internal/model"
)

const utcStamp = "20060102T150405Z"

// Render builds the VCALENDAR body. loc places the stored naive local times;
// now stamps DTSTAMP. Cancelled/deleted items must be filtered out by the
// caller (they simply won't appear, which removes them from HA on next poll).
func Render(items []model.Appointment, loc *time.Location, now time.Time) []byte {
	var b strings.Builder
	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:-//family-visits//EN")
	writeLine(&b, "CALSCALE:GREGORIAN")

	stamp := now.UTC().Format(utcStamp)
	for _, a := range items {
		start, err := a.Start(loc)
		if err != nil {
			continue // unparseable start — skip rather than emit a broken VEVENT
		}
		end := start.Add(time.Hour)
		if a.EndsAt != "" {
			if e, err := time.ParseInLocation(model.LocalDatetime, a.EndsAt, loc); err == nil {
				end = e
			}
		}

		writeLine(&b, "BEGIN:VEVENT")
		writeLine(&b, "UID:visit-"+fmt.Sprint(a.ID)+"@family-visits")
		writeLine(&b, "DTSTAMP:"+stamp)
		writeLine(&b, "DTSTART:"+start.UTC().Format(utcStamp))
		writeLine(&b, "DTEND:"+end.UTC().Format(utcStamp))
		writeLine(&b, "SUMMARY:"+escape(summary(a)))
		if a.Raw != "" {
			writeLine(&b, "DESCRIPTION:"+escape(a.Raw))
		}
		writeLine(&b, "END:VEVENT")
	}

	writeLine(&b, "END:VCALENDAR")
	return []byte(b.String())
}

func summary(a model.Appointment) string {
	if a.Person != "" {
		return a.Title + " · " + a.Person
	}
	return a.Title
}

// writeLine emits a content line with the CRLF terminator ICS requires.
func writeLine(b *strings.Builder, s string) {
	b.WriteString(s)
	b.WriteString("\r\n")
}

// escape applies RFC 5545 TEXT escaping (backslash, semicolon, comma, newline).
func escape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`;`, `\;`,
		`,`, `\,`,
		"\n", `\n`,
		"\r", "",
	)
	return r.Replace(s)
}
