// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/remote"
)

func (c *client) GetEvent(remoteUserID, eventID string) (*remote.Event, error) {
	return nil, errors.New("gcal GetEvent not implemented")
}

// CreateEvent creates a calendar event
func (c *client) CreateEvent(in *remote.Event) (*remote.Event, error) {
	service, err := calendar.NewService(context.Background(), option.WithHTTPClient(c.httpClient))
	if err != nil {
		return nil, errors.Wrap(err, "gcal CreateEvent, error creating service")
	}

	evt := convertRemoteEventToGcalEvent(in)

	resultEvent, err := service.Events.
		Insert(defaultCalendarName, evt).
		SendUpdates("all"). // Send notifications to all attendees.
		Do()
	if err != nil {
		return nil, errors.Wrap(err, "gcal CreateEvent")
	}

	return convertGCalEventToRemoteEvent(resultEvent, c.createdEventLocation(resultEvent)), nil
}

// createdEventLocation returns the location to read the all-day dates of a created event in: the event's own
// time zone if it has one, otherwise the calendar time zone. Google leaves the time zone of date-only events
// empty, so the calendar is fetched only for those; timed events carry their offset and do not need it.
func (c *client) createdEventLocation(event *calendar.Event) *time.Location {
	if event.Start.TimeZone != "" || event.Start.Date == "" {
		return c.calendarLocation(event.Start.TimeZone)
	}

	cal, err := c.getDefaultGoogleCalendar()
	if err != nil {
		if c.Logger != nil {
			c.Warnf("gcal CreateEvent: reading all-day event dates in UTC: %v", err)
		}
		return time.UTC
	}
	return c.calendarLocation(cal.TimeZone)
}

func (c *client) AcceptEvent(remoteUserID, eventID string) error {
	return errors.New("gcal AcceptEvent not implemented")
}

func (c *client) DeclineEvent(remoteUserID, eventID string) error {
	return errors.New("gcal DeclineEvent not implemented")
}

func (c *client) TentativelyAcceptEvent(eventID string) error {
	return errors.New("gcal TentativelyAcceptEvent not implemented")
}

func (c *client) GetEventsBetweenDates(_ string, start, end time.Time) (events []*remote.Event, err error) {
	ctx := context.Background()
	service, err := calendar.NewService(ctx, option.WithHTTPClient(c.httpClient))
	if err != nil {
		return nil, errors.Wrap(err, "gcal GetEventsBetweenDates, error creating service")
	}

	result, err := service.Events.
		List(defaultCalendarName).
		EventTypes(c.statusSyncEventTypes()...).
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		OrderBy("startTime").
		SingleEvents(true).
		ShowDeleted(false).
		ShowHiddenInvitations(false).
		Context(ctx).
		Do()
	if err != nil {
		return nil, errors.Wrap(err, "error getting list of events")
	}

	// All-day events start at midnight in the calendar time zone, not in UTC.
	loc := c.calendarLocation(result.TimeZone)
	for _, evt := range result.Items {
		events = append(events, convertGCalEventToRemoteEvent(evt, loc))
	}

	return events, nil
}

// statusSyncEventTypes returns the event types to fetch for status sync and reminders:
// default events, plus every type named by a status rule that Google's events.list accepts.
// Other types in the rules are ignored, since Google would reject the request.
func (c *client) statusSyncEventTypes() []string {
	types := []string{GoogleEventTypeDefault}
	if c.conf == nil {
		return types
	}
	seen := map[string]bool{GoogleEventTypeDefault: true}
	for _, rule := range c.conf.ParsedStatusRules {
		eventType, ok := googleListEventTypes[strings.ToLower(rule.EventType)]
		if ok && !seen[eventType] {
			seen[eventType] = true
			types = append(types, eventType)
		}
	}
	return types
}

func convertRemoteEventToGcalEvent(in *remote.Event) *calendar.Event {
	out := &calendar.Event{}
	out.Summary = in.Subject
	if in.IsAllDay {
		out.Start, out.End = convertRemoteAllDayEventToGcalDates(in)
	} else {
		out.Start = convertRemoteDateTimeToGcalEventDateTime(in.Start)
		out.End = convertRemoteDateTimeToGcalEventDateTime(in.End)
	}
	if in.Body != nil {
		out.Description = in.Body.Content
	}

	if in.Location != nil {
		out.Location = in.Location.DisplayName
	}

	for _, attendee := range in.Attendees {
		outAttendee := &calendar.EventAttendee{
			Id: attendee.RemoteID,
		}
		if attendee.EmailAddress != nil {
			outAttendee.Email = attendee.EmailAddress.Address
		}
		out.Attendees = append(out.Attendees, outAttendee)
	}

	return out
}

// convertRemoteAllDayEventToGcalDates maps an all-day event onto Google's date-only
// fields. Remote events carry an inclusive end (the last moment of the final day),
// while Google expects the end date to be exclusive.
func convertRemoteAllDayEventToGcalDates(in *remote.Event) (start, end *calendar.EventDateTime) {
	startTime := in.Start.Time()

	endTime := startTime
	if in.End != nil {
		if t := in.End.Time(); t.After(startTime) {
			endTime = t
		}
	}

	return &calendar.EventDateTime{Date: startTime.Format(gcalDateFormat)},
		&calendar.EventDateTime{Date: endTime.AddDate(0, 0, 1).Format(gcalDateFormat)}
}

func convertRemoteDateTimeToGcalEventDateTime(in *remote.DateTime) *calendar.EventDateTime {
	out := &calendar.EventDateTime{}
	dt := in.String()

	// Avoid setting non RFC3339 strings
	if dt != remote.UndefinedDatetime {
		out.DateTime = dt
	}

	out.TimeZone = in.TimeZone

	return out
}
