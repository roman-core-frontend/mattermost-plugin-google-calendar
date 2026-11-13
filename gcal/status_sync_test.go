// Copyright (c) 2019-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gcal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"

	"github.com/mattermost/mattermost-plugin-mscalendar/calendar/config"
)

func TestAllDayEventInCalendarTimeZone(t *testing.T) {
	evt := createMinimalCalendarEvent()
	evt.Start = &calendar.EventDateTime{Date: "2026-11-14"}
	evt.End = &calendar.EventDateTime{Date: "2026-11-15"}

	moscow := calendarLocation("Europe/Moscow")
	event := convertGCalEventToRemoteEventInLocation(&evt, moscow)
	assert.True(t, event.IsAllDay)
	assert.Equal(t, time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC), event.Start.Time(), "midnight in Moscow")
	assert.Equal(t, time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC), event.End.Time())

	utc := convertGCalEventToRemoteEvent(&evt)
	assert.Equal(t, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), utc.Start.Time(), "unchanged for other callers")

	assert.Equal(t, time.UTC, calendarLocation(""))
	assert.Equal(t, time.UTC, calendarLocation("Not/AZone"))
}

func TestOrganizerSelf(t *testing.T) {
	evt := createMinimalCalendarEvent()
	evt.Organizer.Self = true
	assert.True(t, convertGCalEventToRemoteEvent(&evt).IsOrganizer, "organizer without being listed as attendee")

	evt = createMinimalCalendarEvent()
	evt.Attendees = []*calendar.EventAttendee{{Email: "me@example.com", Self: true}, {Email: "other@example.com"}}
	assert.False(t, convertGCalEventToRemoteEvent(&evt).IsOrganizer, "invited by someone else")

	evt.Attendees[0].Organizer = true
	assert.True(t, convertGCalEventToRemoteEvent(&evt).IsOrganizer)
}

func TestStatusSyncEventTypes(t *testing.T) {
	assert.Equal(t, []string{"default"}, (&client{}).statusSyncEventTypes())
	assert.Equal(t, []string{"default"}, (&client{conf: &config.Config{}}).statusSyncEventTypes(), "no rules")

	rules, err := config.ParseStatusRules(`[
		{"title_contains": "Отпуск", "status": "dnd"},
		{"event_type": "outofoffice", "status": "dnd"}
	]`)
	require.NoError(t, err)
	c := &client{conf: &config.Config{StoredConfig: config.StoredConfig{ParsedStatusRules: rules}}}
	assert.Equal(t, []string{"default", "outOfOffice"}, c.statusSyncEventTypes())

	rules, err = config.ParseStatusRules(`[
		{"event_type": "focusTime", "status": "away"},
		{"event_type": "outOfOffice", "status": "dnd"},
		{"event_type": "workingLocation", "status": "away"}
	]`)
	require.NoError(t, err)
	c.conf.ParsedStatusRules = rules
	assert.Equal(t, []string{"default", "outOfOffice", "focusTime"}, c.statusSyncEventTypes())
}
