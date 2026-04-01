package sync

import (
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
)

func ShouldSkip(event *calendar.Event, ignoreBirthdays bool) (bool, string) {
	if event.Status == "cancelled" {
		return true, "cancelled"
	}
	if event.EventType == "workingLocation" {
		return true, "working location"
	}
	if ignoreBirthdays && event.EventType == "birthday" {
		return true, "birthday"
	}
	if strings.Contains(event.Summary, "[gcalsync]") {
		return true, "blocker event"
	}
	if strings.TrimSpace(event.Summary) == "" {
		return true, "empty summary"
	}
	if event.End == nil && event.EndTimeUnspecified {
		return true, "no end time"
	}
	if len(event.Recurrence) > 0 && recurrenceFullyExpired(event.Recurrence) {
		return true, "expired recurrence"
	}
	return false, ""
}

// recurrenceFullyExpired returns true if every RRULE in the recurrence set has
// an UNTIL date in the past. Events with only EXDATEs/EXRULEs and no RRULEs
// are not considered expired.
func recurrenceFullyExpired(recurrence []string) bool {
	now := time.Now()
	hasRRule := false
	for _, rule := range recurrence {
		if !strings.HasPrefix(rule, "RRULE:") {
			continue
		}
		hasRRule = true
		until := extractUntil(rule)
		if until == "" {
			return false // no UNTIL means it recurs forever
		}
		t, err := parseUntil(until)
		if err != nil {
			return false // can't parse, assume still active
		}
		if t.After(now) {
			return false
		}
	}
	return hasRRule
}

func extractUntil(rrule string) string {
	for _, part := range strings.Split(rrule, ";") {
		if strings.HasPrefix(part, "UNTIL=") {
			return strings.TrimPrefix(part, "UNTIL=")
		}
	}
	return ""
}

func parseUntil(s string) (time.Time, error) {
	// Try full datetime format first (20060102T150405Z)
	if t, err := time.Parse("20060102T150405Z", s); err == nil {
		return t, nil
	}
	// Try date-only format (20060102)
	return time.Parse("20060102", s)
}

func ShouldSkipForDest(event *calendar.Event, destCalendarID string, enterprise bool) (bool, string) {
	if isAttendee(event, destCalendarID) {
		return true, "already an attendee"
	}
	if event.EventType == "outOfOffice" && !enterprise {
		return true, "out-of-office on non-enterprise calendar"
	}
	return false, ""
}

func isAttendee(event *calendar.Event, calendarID string) bool {
	lower := strings.ToLower(calendarID)
	for _, a := range event.Attendees {
		if strings.ToLower(a.Email) == lower {
			return true
		}
	}
	return false
}

func responseStatus(event *calendar.Event, calendarID string) string {
	lower := strings.ToLower(calendarID)
	for _, a := range event.Attendees {
		if strings.ToLower(a.Email) == lower {
			return a.ResponseStatus
		}
	}
	return ""
}
