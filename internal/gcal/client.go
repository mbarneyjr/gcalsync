package gcal

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	PropSourceEventID    = "gcalsync_source_event_id"
	PropSourceCalendarID = "gcalsync_source_calendar_id"

	// Google Calendar API allows 500 requests per 100 seconds per user.
	// We set slightly below that to avoid hitting the limit.
	rateLimitPerSecond = 4.5
	rateLimitBurst     = 5
)

type Client struct {
	Service     *calendar.Service
	AccountName string
	limiter     *rate.Limiter
}

func NewClient(ctx context.Context, httpClient *http.Client, accountName string) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating calendar service for %s: %w", accountName, err)
	}
	return &Client{
		Service:     svc,
		AccountName: accountName,
		limiter:     rate.NewLimiter(rate.Limit(rateLimitPerSecond), rateLimitBurst),
	}, nil
}

// wait blocks until the rate limiter allows a request.
func (c *Client) wait(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}

// defaultWindow returns the time range scanned by event/instance listing:
// from the start of the current month to two months later. ListEvents and
// ListInstances MUST share this window — prune decisions in the sync engine
// compare the two and would be wrong if they drifted.
func defaultWindow() (time.Time, time.Time) {
	now := time.Now()
	timeMin := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return timeMin, timeMin.AddDate(0, 2, 0)
}

func (c *Client) ListEvents(ctx context.Context, calendarID string) ([]*calendar.Event, error) {
	timeMin, timeMax := defaultWindow()

	var all []*calendar.Event
	pageToken := ""
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		call := c.Service.Events.List(calendarID).
			SingleEvents(false).
			TimeMin(timeMin.Format(time.RFC3339)).
			TimeMax(timeMax.Format(time.RFC3339)).
			MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing events for %s: %w", calendarID, err)
		}
		all = append(all, result.Items...)

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return all, nil
}

// FilterBlockers extracts blocker events from an event list. It identifies
// blocker parents by the gcalsync extended property, then also includes any
// instances of those parents (instances don't inherit extended properties).
// This is preferred over a separate API call because it uses the same data set
// as ListEvents, avoiding search index consistency issues.
func FilterBlockers(events []*calendar.Event) []*calendar.Event {
	// First pass: find parent blocker IDs.
	parentIDs := make(map[string]bool)
	for _, ev := range events {
		if ev.RecurringEventId != "" {
			continue
		}
		if ev.ExtendedProperties != nil && ev.ExtendedProperties.Private != nil {
			if _, ok := ev.ExtendedProperties.Private[PropSourceEventID]; ok {
				parentIDs[ev.Id] = true
			}
		}
	}

	// Second pass: collect parents and their instances.
	var blockers []*calendar.Event
	for _, ev := range events {
		if parentIDs[ev.Id] {
			blockers = append(blockers, ev)
		} else if ev.RecurringEventId != "" && parentIDs[ev.RecurringEventId] {
			blockers = append(blockers, ev)
		}
	}
	return blockers
}

func (c *Client) CreateEvent(ctx context.Context, calendarID string, event *calendar.Event) (*calendar.Event, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	created, err := c.Service.Events.Insert(calendarID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("creating event on %s: %w", calendarID, err)
	}
	return created, nil
}

func (c *Client) UpdateEvent(ctx context.Context, calendarID string, eventID string, event *calendar.Event) (*calendar.Event, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	updated, err := c.Service.Events.Update(calendarID, eventID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("updating event %s on %s: %w", eventID, calendarID, err)
	}
	return updated, nil
}

// PatchEvent applies a partial update to an event, leaving unspecified fields
// untouched. Used to cancel a single occurrence of a recurring blocker by
// patching status to "cancelled". A virtual instance that was never materialized
// (or is already cancelled) may return 404/410, which is treated as success.
func (c *Client) PatchEvent(ctx context.Context, calendarID string, eventID string, event *calendar.Event) (*calendar.Event, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	patched, err := c.Service.Events.Patch(calendarID, eventID, event).
		SendNotifications(false).Do()
	if err != nil {
		if apiErr, ok := err.(*googleapi.Error); ok {
			if apiErr.Code == 404 || apiErr.Code == 410 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("patching event %s on %s: %w", eventID, calendarID, err)
	}
	return patched, nil
}

func (c *Client) DeleteEvent(ctx context.Context, calendarID string, eventID string) error {
	if err := c.wait(ctx); err != nil {
		return err
	}
	err := c.Service.Events.Delete(calendarID, eventID).
		SendNotifications(false).Do()
	if err != nil {
		if apiErr, ok := err.(*googleapi.Error); ok {
			if apiErr.Code == 404 || apiErr.Code == 410 {
				return nil
			}
		}
		return fmt.Errorf("deleting event %s from %s: %w", eventID, calendarID, err)
	}
	return nil
}

func (c *Client) GetEvent(ctx context.Context, calendarID string, eventID string) (*calendar.Event, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	ev, err := c.Service.Events.Get(calendarID, eventID).Do()
	if err != nil {
		if apiErr, ok := err.(*googleapi.Error); ok {
			if apiErr.Code == 404 || apiErr.Code == 410 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("getting event %s from %s: %w", eventID, calendarID, err)
	}
	return ev, nil
}

func (c *Client) UpdateInstance(ctx context.Context, calendarID string, instanceID string, event *calendar.Event) (*calendar.Event, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	updated, err := c.Service.Events.Update(calendarID, instanceID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("updating instance %s on %s: %w", instanceID, calendarID, err)
	}
	return updated, nil
}

func (c *Client) ListAllBlockers(ctx context.Context, calendarID string) ([]*calendar.Event, error) {
	var all []*calendar.Event
	pageToken := ""
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		call := c.Service.Events.List(calendarID).
			SingleEvents(false).
			Q("[gcalsync]").
			MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing all blockers for %s: %w", calendarID, err)
		}

		for _, ev := range result.Items {
			if strings.Contains(ev.Summary, "[gcalsync]") {
				all = append(all, ev)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return all, nil
}

// ListInstances expands a recurring event into its live (non-cancelled)
// occurrences within the same time window used by ListEvents. It is used to
// decide whether a recurring event still has anything to block — a series whose
// every in-window occurrence has been cancelled returns zero instances.
func (c *Client) ListInstances(ctx context.Context, calendarID string, eventID string) ([]*calendar.Event, error) {
	timeMin, timeMax := defaultWindow()

	var all []*calendar.Event
	pageToken := ""
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		// A 2-month window cannot hold more than 2500 occurrences of any real
		// series, so a single page always covers it; the paging loop below is
		// defensive only.
		call := c.Service.Events.Instances(calendarID, eventID).
			TimeMin(timeMin.Format(time.RFC3339)).
			TimeMax(timeMax.Format(time.RFC3339)).
			MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			if apiErr, ok := err.(*googleapi.Error); ok {
				if apiErr.Code == 404 || apiErr.Code == 410 {
					return nil, nil // series gone entirely
				}
			}
			return nil, fmt.Errorf("listing instances of %s on %s: %w", eventID, calendarID, err)
		}
		for _, ev := range result.Items {
			if ev.Status != "cancelled" {
				all = append(all, ev)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return all, nil
}
