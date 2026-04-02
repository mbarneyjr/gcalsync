package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

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
	ActionCreate         ActionType = "create"
	ActionUpdate         ActionType = "update"
	ActionDelete         ActionType = "delete"
	ActionUpdateInstance ActionType = "update_instance"
)

// PropertyDiff holds the old and new value of a single event property.
type PropertyDiff struct {
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// PlannedAction represents a single change the sync engine intends to make.
type PlannedAction struct {
	// Metadata (non-modifiable)
	Action            ActionType `json:"action"`
	CalendarID        string     `json:"calendar_id"`
	AccountName       string     `json:"account_name"`
	EventID       string `json:"event_id,omitempty"`
	SourceEventID string `json:"source_event_id"`
	EventType         string     `json:"event_type,omitempty"`
	TimeZone          string     `json:"time_zone,omitempty"`
	OriginalStartTime string     `json:"original_start_time,omitempty"` // instance occurrence key
	ParentEventID     string     `json:"parent_event_id,omitempty"`     // existing blocker parent Google ID

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
		case ActionUpdate, ActionUpdateInstance:
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
	summary := a.Summary.New
	if summary == "" {
		summary = a.Summary.Old
	}
	header := fmt.Sprintf("%s/%q (%s)", a.CalendarID, summary, formatHumanTime(*a))

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

	case ActionUpdate, ActionUpdateInstance:
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

			events, err := client.ListEvents(ctx, calID)
			if err != nil {
				log.Printf("[%s/%s] error listing events: %v", accountName, calID, err)
				continue
			}

			blockers, err := client.ListBlockers(ctx, calID)
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

		// Collect modified instances of recurring events.
		type desiredInstance struct {
			parentSourceID string
			summary        string
			description    string
			start          string
			end            string
			eventType      string
			timeZone       string
			responseStatus string
			originalStart  string
		}
		desiredInstances := make(map[string]*desiredInstance) // keyed by source instance event ID

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

				// Collect modified instances separately for instance-level overrides.
				if ev.RecurringEventId != "" {
					status := responseStatus(ev, srcCalID)
					di, exists := desiredInstances[ev.Id]
					if !exists {
						desiredInstances[ev.Id] = &desiredInstance{
							parentSourceID: ev.RecurringEventId,
							summary:        ev.Summary,
							description:    ev.Description,
							start:          formatEventDateTime(ev.Start),
							end:            formatEventDateTime(ev.End),
							eventType:      ev.EventType,
							timeZone:       eventTimeZone(ev),
							responseStatus: status,
							originalStart:  formatEventDateTime(ev.OriginalStartTime),
						}
					} else {
						di.responseStatus = strongestStatus(di.responseStatus, status)
					}
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

		// Index existing blocker parents by source event ID and by Google event ID.
		blockerBySourceID := make(map[string]*calendar.Event)
		blockerByGoogleID := make(map[string]*calendar.Event)
		for _, b := range allBlockers[destCalID] {
			if b.RecurringEventId != "" {
				continue // skip blocker instances, index them below
			}
			if b.ExtendedProperties != nil && b.ExtendedProperties.Private != nil {
				if srcID := b.ExtendedProperties.Private[gcal.PropSourceEventID]; srcID != "" {
					blockerBySourceID[srcID] = b
					blockerByGoogleID[b.Id] = b
				}
			}
		}

		// Index existing blocker instances by (parentSourceID, originalStart).
		blockerInstanceByKey := make(map[string]*calendar.Event)
		for _, b := range allBlockers[destCalID] {
			if b.RecurringEventId == "" {
				continue
			}
			parent, ok := blockerByGoogleID[b.RecurringEventId]
			if !ok || parent.ExtendedProperties == nil || parent.ExtendedProperties.Private == nil {
				continue
			}
			parentSourceID := parent.ExtendedProperties.Private[gcal.PropSourceEventID]
			origStart := formatEventDateTime(b.OriginalStartTime)
			blockerInstanceByKey[parentSourceID+"|"+origStart] = b
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
					Start:         PropertyDiff{Old: formatEventDateTime(blocker.Start)},
					End:           PropertyDiff{Old: formatEventDateTime(blocker.End)},
					Recurrence:    PropertyDiff{Old: formatRecurrence(blocker.Recurrence)},
				})
			}
		}

		// Process modified instances — create instance-level overrides where
		// the instance properties differ from the parent blocker defaults.
		for _, di := range desiredInstances {
			parentDesired, hasParent := desiredByID[di.parentSourceID]
			if !hasParent {
				if e.Verbose {
					log.Printf("[%s] skipping modified instance (parent skipped): %q", destCalID, di.summary)
				}
				continue
			}

			instanceSummary := di.summary + BlockerSuffix
			visibility := e.Config.General.BlockEventVisibility

			key := di.parentSourceID + "|" + di.originalStart
			existingInstance, instanceExists := blockerInstanceByKey[key]

			if instanceExists {
				// Diff against existing blocker instance.
				action := PlannedAction{
					Action:            ActionUpdateInstance,
					CalendarID:        destCalID,
					AccountName:       destInfo.accountName,
					EventID:           existingInstance.Id,
					SourceEventID:     di.parentSourceID,
					OriginalStartTime: di.originalStart,
					EventType:         di.eventType,
					TimeZone:          di.timeZone,
					Start:             PropertyDiff{Old: formatEventDateTime(existingInstance.Start), New: di.start},
					End:               PropertyDiff{Old: formatEventDateTime(existingInstance.End), New: di.end},
					Summary:           PropertyDiff{Old: existingInstance.Summary, New: instanceSummary},
					Description:       PropertyDiff{Old: existingInstance.Description, New: di.description},
					Color:             PropertyDiff{Old: existingInstance.ColorId, New: BlockerColorID},
					Visibility:        PropertyDiff{Old: existingInstance.Visibility, New: visibility},
					ResponseStatus:    PropertyDiff{Old: existingResponseStatus(existingInstance, destCalID), New: di.responseStatus},
				}
				if action.hasChanges() {
					plan.Actions = append(plan.Actions, action)
				}
			} else {
				// New instance override — diff against parent defaults.
				// ParentEventID is set if the blocker parent already exists;
				// left empty if the parent is being created in this same plan
				// (resolved at apply time).
				parentEventID := ""
				if existing := blockerBySourceID[di.parentSourceID]; existing != nil {
					parentEventID = existing.Id
				}

				action := PlannedAction{
					Action:            ActionUpdateInstance,
					CalendarID:        destCalID,
					AccountName:       destInfo.accountName,
					ParentEventID:     parentEventID,
					SourceEventID:     di.parentSourceID,
					OriginalStartTime: di.originalStart,
					EventType:         di.eventType,
					TimeZone:          di.timeZone,
					Start:             PropertyDiff{Old: di.start, New: di.start},
					End:               PropertyDiff{Old: di.end, New: di.end},
					Summary:           PropertyDiff{Old: parentDesired.summary + BlockerSuffix, New: instanceSummary},
					Description:       PropertyDiff{Old: parentDesired.description, New: di.description},
					Color:             PropertyDiff{Old: BlockerColorID, New: BlockerColorID},
					Visibility:        PropertyDiff{Old: visibility, New: visibility},
					ResponseStatus:    PropertyDiff{Old: parentDesired.responseStatus, New: di.responseStatus},
				}
				if action.hasChanges() {
					plan.Actions = append(plan.Actions, action)
				}
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
			events, err := client.ListEvents(ctx, calendarID)
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
			blockers, err := client.ListAllBlockers(ctx, calID)
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
					End:         PropertyDiff{Old: formatEventDateTime(b.End)},
					Recurrence:  PropertyDiff{Old: formatRecurrence(b.Recurrence)},
				})
			}
		}
	}

	return plan
}

// Apply executes all actions in the plan. The plan is the sole source of truth —
// no additional reads from Google Calendar are made.
//
// Execution is two-pass:
//  1. create/update/delete actions — builds a resolution map of created parent IDs
//  2. update_instance actions — resolves instance IDs from parent blocker IDs
func (e *Engine) Apply(ctx context.Context, plan *SyncPlan) *ApplyResult {
	result := &ApplyResult{}
	var mu sync.Mutex // protects result fields

	// Separate instance actions from regular actions.
	var regular, instances []PlannedAction
	for _, a := range plan.Actions {
		if a.Action == ActionUpdateInstance {
			instances = append(instances, a)
		} else {
			regular = append(regular, a)
		}
	}

	// Pass 1: create/update/delete — all actions run concurrently.
	// The per-account rate limiter on each Client handles throttling.
	// Track created recurring blocker IDs for instance resolution.
	type calSource struct {
		calendarID    string
		sourceEventID string
	}
	var parentMu sync.Mutex // protects createdParentIDs
	createdParentIDs := make(map[calSource]string)

	var wg sync.WaitGroup
	for _, a := range regular {
		a := a // capture loop variable
		client := e.Clients[a.AccountName]
		if client == nil {
			mu.Lock()
			result.Errors = append(result.Errors, SyncError{
				Calendar: a.CalendarID,
				EventID:  a.EventID,
				Err:      fmt.Errorf("no client for account %q", a.AccountName),
			})
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			summary := a.Summary.New
			if summary == "" {
				summary = a.Summary.Old
			}
			logLine := fmt.Sprintf("%s %s/%q (%s)", a.Action, a.CalendarID, summary, formatHumanTime(a))

			switch a.Action {
			case ActionCreate:
				ev := buildEventFromAction(a)
				created, err := client.CreateEvent(ctx, a.CalendarID, ev)
				if err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, SyncError{
						Calendar: a.CalendarID,
						Err:      fmt.Errorf("creating blocker for %q: %w", a.Summary.New, err),
					})
					mu.Unlock()
					return
				}
				if a.Recurrence.New != "" {
					parentMu.Lock()
					createdParentIDs[calSource{a.CalendarID, a.SourceEventID}] = created.Id
					parentMu.Unlock()
				}
				log.Println(logLine)
				mu.Lock()
				result.Created++
				mu.Unlock()

			case ActionUpdate:
				ev := buildEventFromAction(a)
				if _, err := client.UpdateEvent(ctx, a.CalendarID, a.EventID, ev); err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, SyncError{
						Calendar: a.CalendarID,
						EventID:  a.EventID,
						Err:      fmt.Errorf("updating blocker for %q: %w", a.Summary.New, err),
					})
					mu.Unlock()
					return
				}
				log.Println(logLine)
				mu.Lock()
				result.Updated++
				mu.Unlock()

			case ActionDelete:
				if err := client.DeleteEvent(ctx, a.CalendarID, a.EventID); err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, SyncError{
						Calendar: a.CalendarID,
						EventID:  a.EventID,
						Err:      fmt.Errorf("deleting blocker %q: %w", a.Summary.Old, err),
					})
					mu.Unlock()
					return
				}
				log.Println(logLine)
				mu.Lock()
				result.Deleted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Pass 2: update_instance actions — all run concurrently.
	// Must wait for pass 1 to complete so createdParentIDs is fully populated.
	for _, a := range instances {
		a := a
		client := e.Clients[a.AccountName]
		if client == nil {
			mu.Lock()
			result.Errors = append(result.Errors, SyncError{
				Calendar: a.CalendarID,
				Err:      fmt.Errorf("no client for account %q", a.AccountName),
			})
			mu.Unlock()
			continue
		}

		// Resolve the instance ID.
		instanceID := a.EventID // set when blocker instance already existed
		if instanceID == "" {
			parentID := a.ParentEventID
			if parentID == "" {
				parentID = createdParentIDs[calSource{a.CalendarID, a.SourceEventID}]
			}
			if parentID == "" {
				mu.Lock()
				result.Errors = append(result.Errors, SyncError{
					Calendar: a.CalendarID,
					Err:      fmt.Errorf("cannot resolve parent blocker for instance %q", a.Summary.New),
				})
				mu.Unlock()
				continue
			}
			instanceID = computeInstanceID(parentID, a.OriginalStartTime)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			ev := buildEventFromAction(a)
			summary := a.Summary.New
			if summary == "" {
				summary = a.Summary.Old
			}
			logLine := fmt.Sprintf("%s %s/%q (%s)", a.Action, a.CalendarID, summary, formatHumanTime(a))

			if _, err := client.UpdateEvent(ctx, a.CalendarID, instanceID, ev); err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, SyncError{
					Calendar: a.CalendarID,
					EventID:  instanceID,
					Err:      fmt.Errorf("updating instance for %q: %w", a.Summary.New, err),
				})
				mu.Unlock()
				return
			}
			log.Println(logLine)
			mu.Lock()
			result.Updated++
			mu.Unlock()
		}()
	}
	wg.Wait()

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
	}

	// Only set extended properties on parent events, not instance overrides.
	if a.Action != ActionUpdateInstance {
		ev.ExtendedProperties = &calendar.EventExtendedProperties{
			Private: map[string]string{
				gcal.PropSourceEventID: a.SourceEventID,
			},
		}
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

// computeInstanceID builds a Google Calendar instance ID from a parent event ID
// and the original start time of the occurrence.
// Format: {parentID}_{YYYYMMDDTHHMMSSZ} for timed events, {parentID}_{YYYYMMDD} for all-day.
func computeInstanceID(parentID, originalStart string) string {
	if parentID == "" || originalStart == "" {
		return ""
	}
	// All-day: YYYY-MM-DD -> YYYYMMDD
	if len(originalStart) == 10 && !strings.Contains(originalStart, "T") {
		return parentID + "_" + strings.ReplaceAll(originalStart, "-", "")
	}
	// Timed: parse RFC3339, convert to UTC compact form
	t, err := time.Parse(time.RFC3339, originalStart)
	if err != nil {
		return ""
	}
	return parentID + "_" + t.UTC().Format("20060102T150405Z")
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
