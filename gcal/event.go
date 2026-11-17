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

	// The plugin creates events with date-times, so there are no all-day dates to read in a time zone.
	return convertGCalEventToRemoteEvent(resultEvent, time.UTC), nil
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

func convertRemoteEventToGcalEvent(in *remote.Event) *calendar.Event {
	out := &calendar.Event{}
	out.Summary = in.Subject
	out.Start = convertRemoteDateTimeToGcalEventDateTime(in.Start)
	out.End = convertRemoteDateTimeToGcalEventDateTime(in.End)
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

// statusSyncEventTypes returns the event types fetched for status sync and reminders. Without status rules only
// default events are fetched. With rules, out-of-office and focus time events are fetched too, since title rules may
// match them. Working location and birthday events are fetched only when a rule names their type: otherwise a title
// rule could match a colleague's birthday or the all-day working location and outrank real meetings. Events created
// by Gmail from emails (fromGmail: flights, reservations) are never fetched: they list the user as a guest and are
// busy, so they would be treated as meetings.
func (c *client) statusSyncEventTypes() []string {
	types := []string{remote.EventTypeDefault}
	if c.conf == nil || len(c.conf.ParsedStatusRules) == 0 {
		return types
	}
	types = append(types, remote.EventTypeOutOfOffice, remote.EventTypeFocusTime)

	named := map[string]bool{}
	for _, rule := range c.conf.ParsedStatusRules {
		named[strings.ToLower(rule.EventType)] = true
	}
	for _, eventType := range []string{googleEventTypeWorkingLocation, googleEventTypeBirthday} {
		if named[strings.ToLower(eventType)] {
			types = append(types, eventType)
		}
	}
	return types
}
