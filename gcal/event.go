// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"context"
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

// createdEventLocation returns the location to read the dates of a created event in. All-day events are created
// with dates only, which Google treats as floating in the calendar time zone and returns without a time zone, so
// the default calendar's time zone is used, as for every other path. Timed events carry an offset and need none.
func (c *client) createdEventLocation(event *calendar.Event) *time.Location {
	if event.Start == nil || event.Start.Date == "" {
		return time.UTC
	}
	cal, err := c.getDefaultGoogleCalendar()
	if err != nil {
		// The event already exists in Google, so creating it does not fail because of this.
		if c.Logger != nil {
			c.Warnf("gcal: unable to load the default calendar, reading the created all-day event in UTC: %v", err)
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

// Google-only event types, not used by the engine.
const (
	googleEventTypeWorkingLocation = "workingLocation"
	googleEventTypeBirthday        = "birthday"
)

// statusSyncEventTypes are the event types fetched for status sync and reminders. Besides default events they
// include the special types that status rules may match (by type or title); without a matching rule the engine
// ignores them, since they have no guests or are shown as free, and it never sends reminders for them.
// Events created by Gmail from emails (fromGmail: flights, reservations) are left out: they list the user as a
// guest and are busy, so they would be treated as meetings even without any status rule.
var statusSyncEventTypes = []string{
	remote.EventTypeDefault,
	remote.EventTypeOutOfOffice,
	remote.EventTypeFocusTime,
	googleEventTypeWorkingLocation,
	googleEventTypeBirthday,
}

func (c *client) GetEventsBetweenDates(_ string, start, end time.Time) (events []*remote.Event, err error) {
	ctx := context.Background()
	service, err := calendar.NewService(ctx, option.WithHTTPClient(c.httpClient))
	if err != nil {
		return nil, errors.Wrap(err, "gcal GetEventsBetweenDates, error creating service")
	}

	result, err := service.Events.
		List(defaultCalendarName).
		EventTypes(statusSyncEventTypes...).
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
