package gcal

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	PropSourceEventID    = "gcalsync_source_event_id"
	PropSourceCalendarID = "gcalsync_source_calendar_id"
)

type Client struct {
	Service     *calendar.Service
	AccountName string
}

func NewClient(ctx context.Context, httpClient *http.Client, accountName string) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating calendar service for %s: %w", accountName, err)
	}
	return &Client{Service: svc, AccountName: accountName}, nil
}

func (c *Client) ListEvents(calendarID string) ([]*calendar.Event, error) {
	now := time.Now()
	timeMin := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	timeMax := timeMin.AddDate(0, 2, 0)

	var all []*calendar.Event
	pageToken := ""
	for {
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

func (c *Client) ListBlockers(calendarID string) ([]*calendar.Event, error) {
	now := time.Now()
	timeMin := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	timeMax := timeMin.AddDate(0, 2, 0)

	var all []*calendar.Event
	pageToken := ""
	for {
		call := c.Service.Events.List(calendarID).
			SingleEvents(false).
			Q("[gcalsync]").
			TimeMin(timeMin.Format(time.RFC3339)).
			TimeMax(timeMax.Format(time.RFC3339)).
			MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing blockers for %s: %w", calendarID, err)
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

func (c *Client) CreateEvent(calendarID string, event *calendar.Event) (*calendar.Event, error) {
	created, err := c.Service.Events.Insert(calendarID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("creating event on %s: %w", calendarID, err)
	}
	return created, nil
}

func (c *Client) UpdateEvent(calendarID string, eventID string, event *calendar.Event) (*calendar.Event, error) {
	updated, err := c.Service.Events.Update(calendarID, eventID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("updating event %s on %s: %w", eventID, calendarID, err)
	}
	return updated, nil
}

func (c *Client) DeleteEvent(calendarID string, eventID string) error {
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

func (c *Client) GetEvent(calendarID string, eventID string) (*calendar.Event, error) {
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

func (c *Client) UpdateInstance(calendarID string, instanceID string, event *calendar.Event) (*calendar.Event, error) {
	updated, err := c.Service.Events.Update(calendarID, instanceID, event).
		SendNotifications(false).Do()
	if err != nil {
		return nil, fmt.Errorf("updating instance %s on %s: %w", instanceID, calendarID, err)
	}
	return updated, nil
}

func (c *Client) ListAllBlockers(calendarID string) ([]*calendar.Event, error) {
	var all []*calendar.Event
	pageToken := ""
	for {
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
