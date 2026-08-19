package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAgendaInheritsTemplateAndSavesDailyOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lehrerin.json")
	handler := NewPersistentServer(path)

	dashboard := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, dashboard, "Wednesday, August 12, 2026")
	assertContains(t, dashboard, "Mathematics")
	assertContains(t, dashboard, "Learning objectives")
	assertContains(t, dashboard, "Lunch break")

	form := url.Values{
		"time": {"07:30-08:15"}, "subject": {"History"}, "class": {"8A"},
		"topic": {"Industrial Revolution"}, "phase_1_content": {"Identify three causes"},
		"phase_2_materials": {"Source cards"}, "phase_3_notes": {"Sentence starters"}, "complete": {"on"},
	}
	saved := request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", form)
	assertContains(t, saved, "History")
	assertContains(t, saved, "Industrial Revolution")
	assertContains(t, saved, "Ready")

	reloaded := request(t, NewPersistentServer(path), http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, reloaded, "Identify three causes")
	assertContains(t, reloaded, "Source cards")
	assertContains(t, reloaded, "Sentence starters")
}

func TestYearAndTimetableViews(t *testing.T) {
	handler := NewServer()
	year := request(t, handler, http.MethodGet, "/year", nil)
	assertContains(t, year, "School year 2026/2027")
	assertContains(t, year, "/?date=2027-07-02")

	schedule := request(t, handler, http.MethodGet, "/schedule", nil)
	assertContains(t, schedule, "Weekly timetable")
	assertContains(t, schedule, "Monday-1-time")
	assertContains(t, schedule, "Save timetable")
	assertContains(t, schedule, "Planner settings")
}

func TestAgendaSubjectFilterShowsWholeWeek(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	form := url.Values{}
	setSlot := func(weekday string, slot int, subject, class string) {
		prefix := weekday + "-" + strconv.Itoa(slot) + "-"
		form.Set(prefix+"time", lessonTimes[slot-1])
		form.Set(prefix+"subject", subject)
		form.Set(prefix+"class", class)
	}
	setSlot("Monday", 1, "Mathematics", "7A")
	setSlot("Friday", 3, "Mathematics", "8B")
	setSlot("Tuesday", 2, "English", "9A")
	request(t, server, http.MethodPost, "/schedule", form)

	weekly := request(t, server, http.MethodGet, "/?date=2026-08-12&subject=Mathematics", nil)
	assertContains(t, weekly, "Mathematics at a glance")
	assertContains(t, weekly, "Monday")
	assertContains(t, weekly, "Friday")
	assertContains(t, weekly, "Class 7A")
	assertContains(t, weekly, "Class 8B")
	if strings.Contains(weekly, "Class 9A") {
		t.Fatal("subject week contains an unrelated English lesson")
	}
}

func TestDayAndWeekOverride(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	dashboard := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, dashboard, "Learning objectives")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"School camp"}, "notes": {"Bring hiking boots"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}})

	overridden := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, overridden, "School camp")
	assertContains(t, overridden, "Bring hiking boots")
	assertContains(t, overridden, "Remove event")
	if strings.Contains(overridden, "Learning objectives") {
		t.Fatal("overridden day still shows regular lesson plan")
	}

	// A neighbouring day should be unaffected by a single-day override.
	tuesday := request(t, server, http.MethodGet, "/?date=2026-08-11", nil)
	assertContains(t, tuesday, "Learning objectives")

	// A second event on the same day should show alongside the first.
	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"Photo day"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}})
	both := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, both, "School camp")
	assertContains(t, both, "Photo day")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/clear", nil)
	oneLeft := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	if strings.Contains(oneLeft, "Bring hiking boots") {
		t.Fatal("removed event still shown")
	}
	assertContains(t, oneLeft, "Photo day")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/clear", nil)
	restored := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, restored, "Learning objectives")

	request(t, server, http.MethodPost, "/agenda/2026-08-10/override",
		url.Values{"title": {"Sports week"}, "start": {"2026-08-10"}, "end": {"2026-08-14"}})

	for _, date := range []string{"2026-08-10", "2026-08-11", "2026-08-12", "2026-08-13", "2026-08-14"} {
		body := request(t, server, http.MethodGet, "/?date="+date, nil)
		assertContains(t, body, "Sports week")
	}
}

func TestDayOverrideActivities(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override", url.Values{
		"title": {"Field trip"}, "start": {"2026-08-12"}, "end": {"2026-08-12"},
		"activity_0_time": {"09:00"}, "activity_0_name": {"Museum visit"},
		"activity_0_material": {"Permission slips"}, "activity_0_description": {"Guided tour"},
		"activity_0_notes": {"Bring water bottles"},
		"activity_1_time":  {"12:00"}, "activity_1_name": {"Lunch"},
	})

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, body, "Museum visit")
	assertContains(t, body, "Permission slips")
	assertContains(t, body, "Guided tour")
	assertContains(t, body, "Bring water bottles")
	assertContains(t, body, "Lunch")
}

func TestEditDayOverride(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override", url.Values{
		"title": {"Field trip"}, "notes": {"Meet at the gate"}, "start": {"2026-08-12"}, "end": {"2026-08-12"},
		"activity_0_time": {"09:00"}, "activity_0_name": {"Museum visit"},
	})

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/edit", url.Values{
		"title": {"Field trip (updated)"}, "notes": {"Meet at the main gate"},
		"activity_0_time": {"10:00"}, "activity_0_name": {"Science center"},
	})

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, body, "Field trip (updated)")
	assertContains(t, body, "Meet at the main gate")
	assertContains(t, body, "Science center")
	if strings.Contains(body, "Museum visit") {
		t.Fatal("edited event still shows the old activity")
	}
}

func TestEditEventWithoutActivities(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"Assembly"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}})

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil)
	assertContains(t, body, "Assembly")
	if strings.Contains(body, "data-activities=\"null\"") {
		t.Fatal("event with no activities rendered data-activities as null, which breaks JSON.parse in the edit button")
	}
	assertContains(t, body, "data-activities=\"[]\"")
}

func TestLessonNotesShowUpInLog(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	saved := request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Fire alarm interrupted the lesson"}})
	assertContains(t, saved, "Fire alarm interrupted the lesson")

	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/2/notes",
		url.Values{"text": {"Great class discussion"}})

	notes := request(t, server, http.MethodGet, "/notes", nil)
	assertContains(t, notes, "Fire alarm interrupted the lesson")
	assertContains(t, notes, "Great class discussion")
	assertContains(t, notes, "/?date=2026-08-12#lesson-1")
	assertContains(t, notes, "/?date=2026-08-13#lesson-2")
	assertContains(t, notes, "id=\"notes-search\"")
	assertContains(t, notes, "data-search=\"")

	// notes appear in reverse chronological order (most recent day first)
	if strings.Index(notes, "Great class discussion") > strings.Index(notes, "Fire alarm interrupted the lesson") {
		t.Fatal("notes are not ordered with the most recent day first")
	}
}

func request(t *testing.T, handler http.Handler, method, target string, form url.Values) string {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("%s %s returned %d: %.300s", method, target, response.Code, response.Body.String())
	}
	return response.Body.String()
}

func assertContains(t *testing.T, body, expected string) {
	t.Helper()
	if !strings.Contains(body, expected) {
		t.Fatalf("response does not contain %q", expected)
	}
}
