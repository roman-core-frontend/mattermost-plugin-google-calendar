// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"

	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/config"
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
	// The location cache is global: unique zone names keep the test repeatable in one process.
	badZone := fmt.Sprintf("Not/AZone%d", time.Now().UnixNano())
	otherBadZone := badZone + "Other"

	logger := &warnLogger{}
	c := &client{Logger: logger}

	assert.Equal(t, time.UTC, c.calendarLocation(""))
	assert.Empty(t, logger.warnings, "an empty zone is UTC")

	moscow := c.calendarLocation("Europe/Moscow")
	assert.Equal(t, "Europe/Moscow", moscow.String())
	assert.Same(t, moscow, c.calendarLocation("Europe/Moscow"), "cached")
	assert.Empty(t, logger.warnings)

	assert.Equal(t, time.UTC, c.calendarLocation(badZone))
	require.Len(t, logger.warnings, 1)
	assert.Contains(t, logger.warnings[0], badZone)

	assert.Equal(t, time.UTC, c.calendarLocation(badZone), "the fallback is cached")
	assert.Equal(t, time.UTC, (&client{Logger: &warnLogger{}}).calendarLocation(badZone))
	assert.Len(t, logger.warnings, 1, "warned once per zone")

	// A zone first seen by a client without a logger is still reported by the next client that has one.
	assert.Equal(t, time.UTC, (&client{}).calendarLocation(otherBadZone), "no logger")
	later := &warnLogger{}
	assert.Equal(t, time.UTC, (&client{Logger: later}).calendarLocation(otherBadZone))
	require.Len(t, later.warnings, 1)
	assert.Contains(t, later.warnings[0], otherBadZone)
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
	withRules := func(raw string) *client {
		rules, err := config.ParseStatusRules(raw)
		require.NoError(t, err)
		return &client{conf: &config.Config{StoredConfig: config.StoredConfig{ParsedStatusRules: rules}}}
	}

	assert.Equal(t, []string{"default"}, (&client{}).statusSyncEventTypes())
	assert.Equal(t, []string{"default"}, (&client{conf: &config.Config{}}).statusSyncEventTypes(), "no rules")
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime"},
		withRules(`[{"title_contains": "Отпуск", "status": "dnd"}]`).statusSyncEventTypes(),
		"title rules may match out-of-office and focus time")
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime", "workingLocation"},
		withRules(`[{"event_type": "workingLocation", "status": "away"}]`).statusSyncEventTypes(),
		"working location only when named")
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime"},
		withRules(`[{"event_type": "fromGmail", "status": "dnd"}]`).statusSyncEventTypes(),
		"Gmail events are never fetched")
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime", "workingLocation", "birthday"},
		withRules(`[{"event_type": "Birthday", "emoji": "cake"}, {"event_type": "WORKINGLOCATION", "status": "away"}]`).statusSyncEventTypes(),
		"type names are matched case-insensitively, in a fixed order")
}
