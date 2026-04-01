package sync

import (
	"fmt"
	"strings"
	"time"
)

// formatHumanTime produces a human-friendly time description for a PlannedAction.
// For recurring events it describes the recurrence pattern; for one-off events it
// shows the date/time.
func formatHumanTime(a PlannedAction) string {
	recurrence := a.Recurrence.New
	if recurrence == "" {
		recurrence = a.Recurrence.Old
	}
	if recurrence != "" {
		return formatHumanRecurrence(recurrence, a.Start.New)
	}

	startStr := a.Start.New
	if startStr == "" {
		startStr = a.Start.Old
	}
	if startStr == "" {
		return "unknown time"
	}

	// All-day: YYYY-MM-DD
	if len(startStr) == 10 && !strings.Contains(startStr, "T") {
		t, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			return startStr
		}
		return t.Format("Mon Jan 2, 2006")
	}

	// DateTime: RFC3339
	t, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return startStr
	}
	return t.Format("Mon Jan 2, 2006 3:04pm")
}

// formatHumanRecurrence turns RRULE strings + a start time into something like
// "every Wednesday at 3:30pm" or "every weekday at 9:00am".
func formatHumanRecurrence(recurrence string, startStr string) string {
	// Parse the start time to get the time-of-day component.
	var timeOfDay string
	var allDay bool
	if len(startStr) == 10 && !strings.Contains(startStr, "T") {
		allDay = true
	} else if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			timeOfDay = t.Format("3:04pm")
		}
	}

	// Find the first RRULE line.
	var rrule string
	for _, line := range strings.Split(recurrence, "\n") {
		if strings.HasPrefix(line, "RRULE:") {
			rrule = strings.TrimPrefix(line, "RRULE:")
			break
		}
	}
	if rrule == "" {
		if allDay {
			return "recurring (all-day)"
		}
		if timeOfDay != "" {
			return fmt.Sprintf("recurring at %s", timeOfDay)
		}
		return "recurring"
	}

	params := make(map[string]string)
	for _, part := range strings.Split(rrule, ";") {
		if kv := strings.SplitN(part, "=", 2); len(kv) == 2 {
			params[kv[0]] = kv[1]
		}
	}

	freq := params["FREQ"]
	byDay := params["BYDAY"]
	interval := params["INTERVAL"]

	var desc string
	switch freq {
	case "DAILY":
		if interval != "" && interval != "1" {
			desc = fmt.Sprintf("every %s days", interval)
		} else {
			desc = "every day"
		}
	case "WEEKLY":
		if byDay != "" {
			days := humanDays(byDay)
			if days == "Monday, Tuesday, Wednesday, Thursday, Friday" {
				days = "weekday"
			}
			if interval != "" && interval != "1" {
				desc = fmt.Sprintf("every %s weeks on %s", interval, days)
			} else {
				desc = fmt.Sprintf("every %s", days)
			}
		} else {
			if interval != "" && interval != "1" {
				desc = fmt.Sprintf("every %s weeks", interval)
			} else {
				desc = "every week"
			}
		}
	case "MONTHLY":
		if interval != "" && interval != "1" {
			desc = fmt.Sprintf("every %s months", interval)
		} else {
			desc = "every month"
		}
	case "YEARLY":
		desc = "every year"
	default:
		desc = "recurring"
	}

	if allDay {
		return desc
	}
	if timeOfDay != "" {
		return fmt.Sprintf("%s at %s", desc, timeOfDay)
	}
	return desc
}

var dayNames = map[string]string{
	"MO": "Monday",
	"TU": "Tuesday",
	"WE": "Wednesday",
	"TH": "Thursday",
	"FR": "Friday",
	"SA": "Saturday",
	"SU": "Sunday",
}

func humanDays(byDay string) string {
	parts := strings.Split(byDay, ",")
	var names []string
	for _, p := range parts {
		if name, ok := dayNames[p]; ok {
			names = append(names, name)
		} else {
			names = append(names, p)
		}
	}
	return strings.Join(names, ", ")
}

// formatDiffValue truncates a property value for display: first line only, max 24 chars.
func formatDiffValue(value string) string {
	if i := strings.Index(value, "\n"); i != -1 {
		value = value[:i]
	}
	if len(value) > 24 {
		value = value[:24] + "..."
	}
	return value
}
