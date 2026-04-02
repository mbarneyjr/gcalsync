package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mbarney/gcalsync/internal/config"
	"github.com/mbarney/gcalsync/internal/gcal"
	"google.golang.org/api/calendar/v3"
)

type Engine struct {
	Config  *config.Config
	Clients map[string]*gcal.Client
	Verbose bool
}

type ActionType string

const (
	ActionCreate ActionType = "create"
	ActionUpdate ActionType = "update"
	ActionDelete ActionType = "delete"
)

// PropertyDiff holds the old and new value of a single event property.
type PropertyDiff struct {
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// PlannedAction represents a single change the sync engine intends to make.
type PlannedAction struct {
	// Metadata (non-modifiable)
	Action        ActionType `json:"action"`
	CalendarID    string     `json:"calendar_id"`
	AccountName   string     `json:"account_name"`
	EventID       string     `json:"event_id,omitempty"`
	InstanceID    string     `json:"instance_id,omitempty"`
	SourceEventID string     `json:"source_event_id"`
	EventType     string     `json:"event_type,omitempty"`
	TimeZone      string     `json:"time_zone,omitempty"`

	// Diffable properties
	Start          PropertyDiff `json:"start"`
	End            PropertyDiff `json:"end"`
	Summary        PropertyDiff `json:"summary"`
	Description    PropertyDiff `json:"description"`
	Color          PropertyDiff `json:"color"`
	Recurrence     PropertyDiff `json:"recurrence"`
	Visibility     PropertyDiff `json:"visibility"`
	ResponseStatus PropertyDiff `json:"response_status"`
}

// SyncPlan contains all planned changes.
type SyncPlan struct {
	Actions []PlannedAction `json:"actions"`
}

// Counts returns the number of creates, updates, and deletes in the plan.
func (p *SyncPlan) Counts() (creates, updates, deletes int) {
	for _, a := range p.Actions {
		switch a.Action {
		case ActionCreate:
			creates++
		case ActionUpdate:
			updates++
		case ActionDelete:
			deletes++
		}
	}
	return
}

// IsEmpty returns true if the plan has no actions.
func (p *SyncPlan) IsEmpty() bool {
	return len(p.Actions) == 0
}

// SaveToFile writes the plan as JSON.
func (p *SyncPlan) SaveToFile(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadPlanFromFile reads a plan from a JSON file.
func LoadPlanFromFile(path string) (*SyncPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan SyncPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// ApplyResult contains the outcome of applying a plan.
type ApplyResult struct {
	Created int
	Updated int
	Deleted int
	Errors  []SyncError
}

type SyncError struct {
	Calendar string
	EventID  string
	Err      error
}

// PrintPlan prints a human-readable summary of the plan.
func (p *SyncPlan) PrintPlan(verbose bool) {
	if p.IsEmpty() {
		return
	}

	for _, a := range p.Actions {
		a.print(verbose)
	}

	creates, updates, deletes := p.Counts()
	fmt.Printf("\nPlan: %d to create, %d to update, %d to delete\n", creates, updates, deletes)
}

func (a *PlannedAction) print(verbose bool) {
	header := fmt.Sprintf("%s/%q", a.CalendarID, a.Summary.New)

	fmtVal := formatDiffValue
	if verbose {
		fmtVal = formatDiffValueVerbose
	}

	switch a.Action {
	case ActionCreate:
		fmt.Printf("+ %s\n", header)
		diffs := a.namedDiffs()
		for _, d := range diffs {
			if d.diff.New != "" {
				fmt.Printf("  + %s: %s\n", d.name, fmtVal(d.diff.New))
			}
		}

	case ActionUpdate:
		fmt.Printf("~ %s\n", header)
		diffs := a.namedDiffs()
		for _, d := range diffs {
			if d.diff.Old != d.diff.New {
				fmt.Printf("  ~ %s: %s -> %s\n", d.name, fmtVal(d.diff.Old), fmtVal(d.diff.New))
			}
		}

	case ActionDelete:
		fmt.Printf("- %s\n", header)
	}
}



func (a *PlannedAction) namedDiffs() []struct {
	name string
	diff PropertyDiff
} {
	return []struct {
		name string
		diff PropertyDiff
	}{
		{"start", a.Start},
		{"end", a.End},
		{"summary", a.Summary},
		{"description", a.Description},
		{"color", a.Color},
		{"recurrence", a.Recurrence},
		{"visibility", a.Visibility},
		{"response_status", a.ResponseStatus},
	}
}

// Plan computes all changes needed without making any mutations.
func (e *Engine) Plan(ctx context.Context) *SyncPlan {
	plan := &SyncPlan{}

	// Gather all events and existing blockers from every configured calendar.
	type calInfo struct {
		accountName string
		enterprise  bool
	}
	calInfoMap := make(map[string]calInfo)
	allEvents := make(map[string][]*calendar.Event)    // calID -> source events
	allBlockers := make(map[string][]*calendar.Event)   // calID -> existing blockers

	for accountName, acct := range e.Config.Accounts {
		client := e.Clients[accountName]
		for _, calID := range acct.Calendars {
			calInfoMap[calID] = calInfo{accountName: accountName, enterprise: acct.Enterprise}

			if e.Verbose {
				log.Printf("[%s/%s] listing events", accountName, calID)
			}

			events, err := client.ListEvents(calID)
			if err != nil {
				log.Printf("[%s/%s] error listing events: %v", accountName, calID, err)
				continue
			}

			blockers, err := client.ListBlockers(calID)
			if err != nil {
				log.Printf("[%s/%s] error listing blockers: %v", accountName, calID, err)
				continue
			}

			if e.Verbose {
				log.Printf("[%s/%s] found %d events, %d blockers", accountName, calID, len(events), len(blockers))
			}

			allEvents[calID] = events
			allBlockers[calID] = blockers
		}
	}

	var allCalIDs []string
	for _, acct := range e.Config.Accounts {
		allCalIDs = append(allCalIDs, acct.Calendars...)
	}

	// For each destination calendar, compute the desired state and diff against existing blockers.
	for _, destCalID := range allCalIDs {
		destInfo := calInfoMap[destCalID]

		// Build desired blockers from all source calendars.
		// Key by source event ID, picking the strongest response status across sources.
		type desired struct {
			sourceEventID  string
			summary        string
			description    string
			start          string
			end            string
			eventType      string
			timeZone       string
			recurrence     string
			responseStatus string
			isRecurring    bool
		}
		desiredByID := make(map[string]*desired)

		for _, srcCalID := range allCalIDs {
			if srcCalID == destCalID {
				continue
			}

			for _, ev := range allEvents[srcCalID] {
				if skip, reason := ShouldSkip(ev, e.Config.General.IgnoreBirthdays); skip {
					if e.Verbose {
						log.Printf("[%s -> %s] skipping %q (%s)", srcCalID, destCalID, ev.Summary, reason)
					}
					continue
				}
				if skip, reason := ShouldSkipForDest(ev, destCalID, destInfo.enterprise); skip {
					if e.Verbose {
						log.Printf("[%s -> %s] skipping %q (%s)", srcCalID, destCalID, ev.Summary, reason)
					}
					continue
				}

				// Skip modified instances — they're handled via instance IDs on the parent.
				if ev.RecurringEventId != "" {
					continue
				}

				status := responseStatus(ev, srcCalID)
				d, exists := desiredByID[ev.Id]
				if !exists {
					desiredByID[ev.Id] = &desired{
						sourceEventID:  ev.Id,
						summary:        ev.Summary,
						description:    ev.Description,
						start:          formatEventDateTime(ev.Start),
						end:            formatEventDateTime(ev.End),
						eventType:      ev.EventType,
						timeZone:       eventTimeZone(ev),
						recurrence:     formatRecurrence(ev.Recurrence),
						responseStatus: status,
						isRecurring:    len(ev.Recurrence) > 0,
					}
				} else {
					// Pick the strongest response status across source calendars.
					d.responseStatus = strongestStatus(d.responseStatus, status)
				}
			}
		}

		// Index existing blockers by source event ID.
		blockerBySourceID := make(map[string]*calendar.Event)
		for _, b := range allBlockers[destCalID] {
			if b.ExtendedProperties != nil && b.ExtendedProperties.Private != nil {
				if srcID := b.ExtendedProperties.Private[gcal.PropSourceEventID]; srcID != "" {
					blockerBySourceID[srcID] = b
				}
			}
		}

		// Diff: compare desired state against existing blockers.
		matched := make(map[string]bool)

		for _, d := range desiredByID {
			newSummary := d.summary + BlockerSuffix
			visibility := e.Config.General.BlockEventVisibility

			existing, exists := blockerBySourceID[d.sourceEventID]
			if !exists {
				// Create
				plan.Actions = append(plan.Actions, PlannedAction{
					Action:        ActionCreate,
					CalendarID:    destCalID,
					AccountName:   destInfo.accountName,
					SourceEventID: d.sourceEventID,
					EventType:     d.eventType,
					TimeZone:      d.timeZone,
					Start:         PropertyDiff{New: d.start},
					End:           PropertyDiff{New: d.end},
					Summary:       PropertyDiff{New: newSummary},
					Description:   PropertyDiff{New: d.description},
					Color:         PropertyDiff{New: BlockerColorID},
					Recurrence:    PropertyDiff{New: d.recurrence},
					Visibility:    PropertyDiff{New: visibility},
					ResponseStatus: PropertyDiff{New: d.responseStatus},
				})
				continue
			}

			matched[d.sourceEventID] = true

			// Update — diff each property.
			action := PlannedAction{
				Action:        ActionUpdate,
				CalendarID:    destCalID,
				AccountName:   destInfo.accountName,
				EventID:       existing.Id,
				SourceEventID: d.sourceEventID,
				EventType:     d.eventType,
				TimeZone:      d.timeZone,
				Start:         PropertyDiff{Old: formatEventDateTime(existing.Start), New: d.start},
				End:           PropertyDiff{Old: formatEventDateTime(existing.End), New: d.end},
				Summary:       PropertyDiff{Old: existing.Summary, New: newSummary},
				Description:   PropertyDiff{Old: existing.Description, New: d.description},
				Color:         PropertyDiff{Old: existing.ColorId, New: BlockerColorID},
				Recurrence:    PropertyDiff{Old: formatRecurrence(existing.Recurrence), New: d.recurrence},
				Visibility:    PropertyDiff{Old: existing.Visibility, New: visibility},
				ResponseStatus: PropertyDiff{Old: existingResponseStatus(existing, destCalID), New: d.responseStatus},
			}

			// Only emit if something actually changed.
			if action.hasChanges() {
				plan.Actions = append(plan.Actions, action)
			}
		}

		// Delete — existing blockers with no matching desired state.
		for srcID, blocker := range blockerBySourceID {
			if !matched[srcID] {
				plan.Actions = append(plan.Actions, PlannedAction{
					Action:        ActionDelete,
					CalendarID:    destCalID,
					AccountName:   destInfo.accountName,
					EventID:       blocker.Id,
					SourceEventID: srcID,
					Summary:       PropertyDiff{Old: blocker.Summary},
				})
			}
		}
	}

	return plan
}

func (a *PlannedAction) hasChanges() bool {
	for _, d := range a.namedDiffs() {
		if d.diff.Old != d.diff.New {
			return true
		}
	}
	return false
}

func formatEventDateTime(dt *calendar.EventDateTime) string {
	if dt == nil {
		return ""
	}
	if dt.Date != "" {
		return dt.Date
	}
	return dt.DateTime
}

func formatRecurrence(rules []string) string {
	return strings.Join(rules, "\n")
}

// strongestStatus returns the stronger of two response statuses.
// accepted > tentative > needsAction > declined
func strongestStatus(a, b string) string {
	rank := map[string]int{
		"accepted":    4,
		"tentative":   3,
		"needsAction": 2,
		"declined":    1,
		"":            0,
	}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

func existingResponseStatus(ev *calendar.Event, calendarID string) string {
	for _, a := range ev.Attendees {
		if strings.EqualFold(a.Email, calendarID) {
			return a.ResponseStatus
		}
	}
	return ""
}

// DesyncPlan builds a plan to remove blocker events. If calendarID is empty, it
// removes all blockers from all calendars. If calendarID is set, it removes all
// blockers on that calendar (as a destination) and all blockers on other calendars
// where that calendar is the source.
func (e *Engine) DesyncPlan(ctx context.Context, calendarID string) *SyncPlan {
	plan := &SyncPlan{}

	calAccount := make(map[string]string)
	for accountName, acct := range e.Config.Accounts {
		for _, calID := range acct.Calendars {
			calAccount[calID] = accountName
		}
	}

	// Collect source event IDs from the target calendar (for filtering blockers sourced from it).
	sourceEventIDs := make(map[string]bool)
	if calendarID != "" {
		accountName := calAccount[calendarID]
		if client := e.Clients[accountName]; client != nil {
			events, err := client.ListEvents(calendarID)
			if err != nil {
				log.Printf("[%s] error listing events: %v", calendarID, err)
			} else {
				for _, ev := range events {
					sourceEventIDs[ev.Id] = true
				}
			}
		}
	}

	for accountName, acct := range e.Config.Accounts {
		client := e.Clients[accountName]
		if client == nil {
			continue
		}

		for _, calID := range acct.Calendars {
			blockers, err := client.ListAllBlockers(calID)
			if err != nil {
				log.Printf("[%s] error listing blockers: %v", calID, err)
				continue
			}

			for _, b := range blockers {
				if calendarID != "" {
					// Only delete blockers on the target calendar, or blockers
					// sourced from the target calendar on other calendars.
					onTargetCal := calID == calendarID
					sourcedFromTarget := false
					if b.ExtendedProperties != nil && b.ExtendedProperties.Private != nil {
						srcID := b.ExtendedProperties.Private[gcal.PropSourceEventID]
						sourcedFromTarget = sourceEventIDs[srcID]
					}
					if !onTargetCal && !sourcedFromTarget {
						continue
					}
				}

				plan.Actions = append(plan.Actions, PlannedAction{
					Action:      ActionDelete,
					CalendarID:  calID,
					AccountName: accountName,
					EventID:     b.Id,
					Summary:     PropertyDiff{Old: b.Summary},
					Start:       PropertyDiff{Old: formatEventDateTime(b.Start)},
				})
			}
		}
	}

	return plan
}

// Apply executes all actions in the plan. The plan is the sole source of truth —
// no additional reads from Google Calendar are made.
func (e *Engine) Apply(ctx context.Context, plan *SyncPlan) *ApplyResult {
	result := &ApplyResult{}

	for _, a := range plan.Actions {
		client := e.Clients[a.AccountName]
		if client == nil {
			result.Errors = append(result.Errors, SyncError{
				Calendar: a.CalendarID,
				EventID:  a.EventID,
				Err:      fmt.Errorf("no client for account %q", a.AccountName),
			})
			continue
		}

		summary := a.Summary.New
		if summary == "" {
			summary = a.Summary.Old
		}
		logLine := fmt.Sprintf("%s %s/%q (%s)", a.Action, a.CalendarID, summary, formatHumanTime(a))

		switch a.Action {
		case ActionCreate:
			ev := buildEventFromAction(a)
			if _, err := client.CreateEvent(a.CalendarID, ev); err != nil {
				result.Errors = append(result.Errors, SyncError{
					Calendar: a.CalendarID,
					Err:      fmt.Errorf("creating blocker for %q: %w", a.Summary.New, err),
				})
				continue
			}
			log.Println(logLine)
			result.Created++

		case ActionUpdate:
			ev := buildEventFromAction(a)
			eventID := a.EventID
			if a.InstanceID != "" {
				eventID = a.InstanceID
			}
			if _, err := client.UpdateEvent(a.CalendarID, eventID, ev); err != nil {
				result.Errors = append(result.Errors, SyncError{
					Calendar: a.CalendarID,
					EventID:  eventID,
					Err:      fmt.Errorf("updating blocker for %q: %w", a.Summary.New, err),
				})
				continue
			}
			log.Println(logLine)
			result.Updated++

		case ActionDelete:
			if err := client.DeleteEvent(a.CalendarID, a.EventID); err != nil {
				result.Errors = append(result.Errors, SyncError{
					Calendar: a.CalendarID,
					EventID:  a.EventID,
					Err:      fmt.Errorf("deleting blocker %q: %w", a.Summary.Old, err),
				})
				continue
			}
			log.Println(logLine)
			result.Deleted++
		}
	}

	return result
}

// buildEventFromAction constructs a Google Calendar event from a PlannedAction,
// using the .New values of all diffable properties.
func buildEventFromAction(a PlannedAction) *calendar.Event {
	ev := &calendar.Event{
		Summary:     a.Summary.New,
		Description: a.Description.New,
		ColorId:     a.Color.New,
		Reminders:   &calendar.EventReminders{UseDefault: false, ForceSendFields: []string{"UseDefault"}},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: map[string]string{
				gcal.PropSourceEventID: a.SourceEventID,
			},
		},
	}

	ev.Start = parseEventDateTime(a.Start.New, a.TimeZone)
	ev.End = parseEventDateTime(a.End.New, a.TimeZone)

	if a.Visibility.New != "" {
		ev.Visibility = a.Visibility.New
	}

	if a.Recurrence.New != "" {
		ev.Recurrence = strings.Split(a.Recurrence.New, "\n")
	}

	if a.EventType == "outOfOffice" || a.EventType == "focusTime" {
		ev.EventType = a.EventType
	}

	// Add the destination calendar as attendee with the response status,
	// so the calendar natively shows accepted/tentative/declined.
	if a.ResponseStatus.New != "" && a.EventType != "outOfOffice" {
		ev.Attendees = []*calendar.EventAttendee{{
			Email:          a.CalendarID,
			ResponseStatus: a.ResponseStatus.New,
		}}
	}

	return ev
}

// parseEventDateTime converts a date or datetime string back into a Google Calendar EventDateTime.
func parseEventDateTime(s string, timeZone string) *calendar.EventDateTime {
	if s == "" {
		return nil
	}
	// Date-only format: YYYY-MM-DD (10 chars, no T)
	if len(s) == 10 && !strings.Contains(s, "T") {
		return &calendar.EventDateTime{Date: s, TimeZone: timeZone}
	}
	return &calendar.EventDateTime{DateTime: s, TimeZone: timeZone}
}

// eventTimeZone extracts the timezone from a source event, preferring Start.TimeZone.
func eventTimeZone(ev *calendar.Event) string {
	if ev.Start != nil && ev.Start.TimeZone != "" {
		return ev.Start.TimeZone
	}
	if ev.End != nil && ev.End.TimeZone != "" {
		return ev.End.TimeZone
	}
	return ""
}
