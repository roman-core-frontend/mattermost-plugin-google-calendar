// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"

	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/remote"
	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/utils/bot"
)

// warnLogger records warnings.
type warnLogger struct {
	bot.NilLogger
	warnings []string
}

func (l *warnLogger) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestAllDayEventInCalendarTimeZone(t *testing.T) {
	evt := createMinimalCalendarEvent()
	evt.Start = &calendar.EventDateTime{Date: "2026-11-14"}
	evt.End = &calendar.EventDateTime{Date: "2026-11-15"}

	c := &client{}
	event := convertGCalEventToRemoteEvent(&evt, c.calendarLocation("Europe/Moscow"))
	assert.True(t, event.IsAllDay)
	assert.Equal(t, time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC), event.Start.Time(), "midnight in Moscow")
	assert.Equal(t, time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC), event.End.Time())

	utc := convertGCalEventToRemoteEvent(&evt, time.UTC)
	assert.Equal(t, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), utc.Start.Time())

	timed := createMinimalCalendarEvent()
	assert.Equal(t, time.Date(2023, 8, 1, 0, 5, 0, 0, time.UTC), convertGCalEventToRemoteEvent(&timed, c.calendarLocation("Europe/Moscow")).Start.Time(),
		"date-times are not affected by the location")
}

func TestCalendarLocation(t *testing.T) {
	logger := &warnLogger{}
	c := &client{Logger: logger}

	assert.Equal(t, time.UTC, c.calendarLocation(""))
	assert.Empty(t, logger.warnings, "an empty zone is UTC")

	moscow := c.calendarLocation("Europe/Moscow")
	assert.Equal(t, "Europe/Moscow", moscow.String())
	assert.Same(t, moscow, c.calendarLocation("Europe/Moscow"), "cached")
	assert.Empty(t, logger.warnings)

	assert.Equal(t, time.UTC, c.calendarLocation("Not/AZone"))
	require.Len(t, logger.warnings, 1)
	assert.Contains(t, logger.warnings[0], "Not/AZone")

	assert.Equal(t, time.UTC, c.calendarLocation("Not/AZone"), "the fallback is cached")
	assert.Equal(t, time.UTC, (&client{Logger: &warnLogger{}}).calendarLocation("Not/AZone"))
	assert.Len(t, logger.warnings, 1, "warned once per zone")

	// A zone first seen by a client without a logger is still reported by the next client that has one.
	assert.Equal(t, time.UTC, (&client{}).calendarLocation("Not/AnotherZone"), "no logger")
	later := &warnLogger{}
	assert.Equal(t, time.UTC, (&client{Logger: later}).calendarLocation("Not/AnotherZone"))
	require.Len(t, later.warnings, 1)
	assert.Contains(t, later.warnings[0], "Not/AnotherZone")
}

// fakeGoogle serves the calendar API requests made by CreateEvent.
type fakeGoogle struct {
	event        string
	calendar     string
	calendarErr  bool
	calendarGets int
}

func (f *fakeGoogle) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/calendars/primary/events"):
		status, body = http.StatusOK, f.event
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/calendars/primary"):
		f.calendarGets++
		if !f.calendarErr {
			status, body = http.StatusOK, f.calendar
		}
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestCreateEventTimeZone(t *testing.T) {
	const allDay = `{"id":"1","summary":"Day off","start":{"date":"2026-11-14"},"end":{"date":"2026-11-15"},"organizer":{"email":"me@example.com","self":true}}`
	in := &remote.Event{
		Subject:  "Day off",
		IsAllDay: true,
		Start:    &remote.DateTime{DateTime: "2026-11-14T00:00:00", TimeZone: "America/New_York"},
		End:      &remote.DateTime{DateTime: "2026-11-14T23:59:59", TimeZone: "America/New_York"},
	}
	newClient := func(f *fakeGoogle) *client {
		return &client{httpClient: &http.Client{Transport: f}, Logger: &warnLogger{}}
	}

	t.Run("all-day events are read in the calendar time zone", func(t *testing.T) {
		f := &fakeGoogle{event: allDay, calendar: `{"id":"primary","timeZone":"America/New_York"}`}
		event, err := newClient(f).CreateEvent(in)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC), event.Start.Time(), "midnight in New York")
		assert.Equal(t, 1, f.calendarGets)
	})

	t.Run("all-day dates use the calendar time zone even if the event has its own", func(t *testing.T) {
		f := &fakeGoogle{
			event:    `{"id":"1","start":{"date":"2026-11-14","timeZone":"Europe/Moscow"},"end":{"date":"2026-11-15"}}`,
			calendar: `{"id":"primary","timeZone":"America/New_York"}`,
		}
		event, err := newClient(f).CreateEvent(in)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC), event.Start.Time(), "midnight in New York, as status sync reads it")
		assert.Equal(t, 1, f.calendarGets)
	})

	t.Run("timed events do not fetch the calendar", func(t *testing.T) {
		f := &fakeGoogle{event: `{"id":"1","start":{"dateTime":"2026-11-14T09:00:00-05:00"},"end":{"dateTime":"2026-11-14T10:00:00-05:00"}}`}
		event, err := newClient(f).CreateEvent(in)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC), event.Start.Time())
		assert.Equal(t, 0, f.calendarGets)
	})

	t.Run("falls back to UTC with a warning if the calendar cannot be fetched", func(t *testing.T) {
		f := &fakeGoogle{event: allDay, calendarErr: true}
		c := newClient(f)
		event, err := c.CreateEvent(in)
		require.NoError(t, err, "the event is created anyway")
		assert.Equal(t, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), event.Start.Time())
		assert.Len(t, c.Logger.(*warnLogger).warnings, 1)
	})
}

func TestOrganizerSelf(t *testing.T) {
	evt := createMinimalCalendarEvent()
	evt.Organizer.Self = true
	assert.True(t, convertGCalEventToRemoteEvent(&evt, time.UTC).IsOrganizer, "organizer without being listed as attendee")

	evt = createMinimalCalendarEvent()
	evt.Attendees = []*calendar.EventAttendee{{Email: "me@example.com", Self: true}, {Email: "other@example.com"}}
	assert.False(t, convertGCalEventToRemoteEvent(&evt, time.UTC).IsOrganizer, "invited by someone else")

	evt.Attendees[0].Organizer = true
	assert.True(t, convertGCalEventToRemoteEvent(&evt, time.UTC).IsOrganizer)
}

func TestNilOrganizer(t *testing.T) {
	evt := createMinimalCalendarEvent()
	evt.Organizer = nil
	evt.Attendees = []*calendar.EventAttendee{{Email: "me@example.com", Self: true, Organizer: true}}

	event := convertGCalEventToRemoteEvent(&evt, time.UTC)
	require.NotNil(t, event.Organizer)
	require.NotNil(t, event.Organizer.EmailAddress)
	assert.Empty(t, event.Organizer.EmailAddress.Address)
	assert.True(t, event.IsOrganizer)
}

func TestStatusSyncEventTypes(t *testing.T) {
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime", "workingLocation", "birthday"}, statusSyncEventTypes)
	assert.NotContains(t, statusSyncEventTypes, "fromGmail", "Gmail events would count as meetings without any rule")
}
