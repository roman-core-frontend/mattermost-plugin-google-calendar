// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/remote"
	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/utils"
)

const (
	RemoteEventBusy = "busy"
	RemoteEventFree = "free"

	GoogleEventBusy = "opaque"
	GoogleEventFree = "transparent"

	GoogleResponseStatusYes   = "accepted"
	GoogleResponseStatusMaybe = "tentative"
	GoogleResponseStatusNo    = "declined"
	GoogleResponseStatusNone  = "needsAction"
)

var responseStatusConversion = map[string]string{
	GoogleResponseStatusYes:   remote.EventResponseStatusAccepted,
	GoogleResponseStatusMaybe: remote.EventResponseStatusTentative,
	GoogleResponseStatusNo:    remote.EventResponseStatusDeclined,
	GoogleResponseStatusNone:  remote.EventResponseStatusNotAnswered,
}

func (c *client) GetDefaultCalendarView(_ string, start, end time.Time) ([]*remote.Event, error) {
	service, err := calendar.NewService(context.Background(), option.WithHTTPClient(c.httpClient))
	if err != nil {
		return nil, errors.Wrap(err, "gcal GetDefaultCalendarView, error creating service")
	}

	req := service.Events.
		List(defaultCalendarName).
		EventTypes(remote.EventTypeDefault).
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		SingleEvents(true).
		ShowDeleted(false).
		ShowHiddenInvitations(false).
		OrderBy("startTime").
		Context(context.TODO())

	result, err := req.Do()
	if err != nil {
		return nil, errors.Wrap(err, "gcal GetDefaultCalendarView, error performing request")
	}

	if len(result.Items) == 0 {
		return []*remote.Event{}, nil
	}

	// All-day events start at midnight in the calendar time zone, not in UTC.
	loc := c.calendarLocation(result.TimeZone)
	events := []*remote.Event{}
	for _, event := range result.Items {
		if event.ICalUID != "" {
			events = append(events, convertGCalEventToRemoteEvent(event, loc))
		}
	}

	return events, nil
}

// convertGCalEventDateTimeToRemoteDateTime converts an event date or date-time to UTC.
// All-day events only have a date, which starts at midnight in the given location (the calendar time zone).
func convertGCalEventDateTimeToRemoteDateTime(dt *calendar.EventDateTime, loc *time.Location) *remote.DateTime {
	// Handle all-day events
	if len(dt.Date) > 0 {
		t, _ := time.ParseInLocation("2006-01-02", dt.Date, loc)
		return remote.NewDateTime(t.UTC(), "UTC")
	}

	t, _ := time.Parse(time.RFC3339, dt.DateTime)
	return remote.NewDateTime(t.UTC(), "UTC")
}

// cachedLocation is a loaded calendar time zone, or UTC with the error if the zone cannot be loaded.
type cachedLocation struct {
	loc *time.Location
	err error
}

// locationCache caches calendar time zones by name; warnedZones remembers the unknown zones already reported.
var (
	locationCache sync.Map // map[string]cachedLocation
	warnedZones   sync.Map // map[string]bool
)

// calendarLocation returns the location of a calendar time zone. An empty zone is UTC. A zone that cannot be
// loaded falls back to UTC, with a warning logged once per zone (by the first client that has a logger).
func (c *client) calendarLocation(timeZone string) *time.Location {
	if timeZone == "" {
		return time.UTC
	}

	cached, ok := locationCache.Load(timeZone)
	if !ok {
		loc, err := time.LoadLocation(timeZone)
		if err != nil {
			loc = time.UTC
		}
		cached, _ = locationCache.LoadOrStore(timeZone, cachedLocation{loc: loc, err: err})
	}

	cl := cached.(cachedLocation)
	if cl.err != nil && c.Logger != nil {
		if _, warned := warnedZones.LoadOrStore(timeZone, true); !warned {
			c.Warnf("gcal: unknown calendar time zone %q, reading all-day events in UTC: %v", timeZone, cl.err)
		}
	}
	return cl.loc
}

// convertGCalEventToRemoteEvent converts an event, reading all-day dates in the given location
// (usually the calendar time zone).
func convertGCalEventToRemoteEvent(event *calendar.Event, loc *time.Location) *remote.Event {
	showAs := RemoteEventBusy
	if event.Transparency == GoogleEventFree {
		showAs = RemoteEventFree
	}

	start := convertGCalEventDateTimeToRemoteDateTime(event.Start, loc)
	end := convertGCalEventDateTimeToRemoteDateTime(event.End, loc)

	var conference *remote.Conference
	var location *remote.Location

	if event.ConferenceData != nil && len(event.ConferenceData.EntryPoints) > 0 {
		conference = &remote.Conference{
			URL: event.ConferenceData.EntryPoints[0].Uri,
		}
		if event.ConferenceData.ConferenceSolution != nil {
			conference.Application = event.ConferenceData.ConferenceSolution.Name
		}
	} else if utils.IsURL(event.Location) {
		conference = &remote.Conference{
			URL: event.Location,
		}
	}

	if !utils.IsURL(event.Location) {
		location = &remote.Location{
			DisplayName: event.Location,
		}
	}

	// The engine expects an organizer with an email address, so an event without one gets an empty address.
	organizer := &remote.Attendee{
		EmailAddress: &remote.EmailAddress{},
	}
	if event.Organizer != nil {
		organizer.EmailAddress.Name = event.Organizer.Email
		organizer.EmailAddress.Address = event.Organizer.Email
	}

	responseStatus := &remote.EventResponseStatus{
		Response: remote.EventResponseStatusNotAnswered,
	}
	responseRequested := false
	// The organizer is not always listed among the attendees (e.g. events without guests).
	isOrganizer := event.Organizer != nil && event.Organizer.Self

	attendees := []*remote.Attendee{}
	for _, attendee := range event.Attendees {
		attendees = append(attendees, &remote.Attendee{
			Status: &remote.EventResponseStatus{
				Response: attendee.ResponseStatus,
			},
			EmailAddress: &remote.EmailAddress{
				Name:    attendee.Email,
				Address: attendee.Email,
			},
		})

		if attendee.Self {
			if attendee.ResponseStatus == GoogleResponseStatusNone {
				responseRequested = true
			}

			response := responseStatusConversion[attendee.ResponseStatus]
			responseStatus.Response = response

			isOrganizer = isOrganizer || attendee.Organizer
		}
	}

	isAllDay := len(event.Start.Date) > 0 // if Date field is present, it is all-day. as opposed to DateTime field

	return &remote.Event{
		ID:                event.Id,
		ICalUID:           event.ICalUID,
		Subject:           event.Summary,
		Body:              &remote.ItemBody{Content: event.Description},
		BodyPreview:       event.Description, // GCAL TODO no body preview available?
		IsAllDay:          isAllDay,
		ShowAs:            showAs,
		EventType:         event.EventType,
		Weblink:           event.HtmlLink,
		Start:             start,
		End:               end,
		Location:          location,
		Conference:        conference,
		Organizer:         organizer,
		Attendees:         attendees,
		ResponseStatus:    responseStatus,
		IsCancelled:       event.Status == "cancelled",
		IsOrganizer:       isOrganizer,
		ResponseRequested: responseRequested,
		// 	Importance                 string
		// 	ReminderMinutesBeforeStart int
	}
}

func (c *client) DoBatchViewCalendarRequests(_ []*remote.ViewCalendarParams) ([]*remote.ViewCalendarResponse, error) {
	return nil, remote.ErrNotImplemented
}
