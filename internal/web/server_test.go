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

func TestNotesCanBeTaggedAndFilteredByStudent(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Struggled with fractions"}, "student": {"Alex Doe"}})
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/2/notes",
		url.Values{"text": {"Great class discussion"}})

	all := request(t, server, http.MethodGet, "/notes", nil)
	assertContains(t, all, "Struggled with fractions")
	assertContains(t, all, "Alex Doe")
	assertContains(t, all, "Great class discussion")
	// the student never added to Settings should still appear as a filter option
	assertContains(t, all, "<option value=\"Alex Doe\"")

	filtered := request(t, server, http.MethodGet, "/notes?student=Alex+Doe", nil)
	assertContains(t, filtered, "Struggled with fractions")
	if strings.Contains(filtered, "Great class discussion") {
		t.Fatal("student filter should hide notes tagged for someone else (or untagged)")
	}
}

func TestResetDataWipesEverything(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lehrerin.json")
	handler := NewPersistentServer(path)

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Struggled with fractions"}, "student": {"Alex Doe"}})
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"School camp"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}})
	request(t, handler, http.MethodPost, "/settings",
		url.Values{"teacher": {"Ms. Weber"}, "students": {"Alex Doe"}})

	request(t, handler, http.MethodPost, "/reset", nil)

	notes := request(t, handler, http.MethodGet, "/notes", nil)
	if strings.Contains(notes, "Struggled with fractions") {
		t.Fatal("reset should clear existing notes")
	}

	agenda := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil)
	if strings.Contains(agenda, "day-override-list") {
		t.Fatal("reset should clear existing special day events")
	}

	// data should also be reset for anyone reloading from the persisted file
	reloaded := request(t, NewPersistentServer(path), http.MethodGet, "/notes", nil)
	if strings.Contains(reloaded, "Struggled with fractions") {
		t.Fatal("reset should persist the wiped data to disk")
	}
}

func TestReviewShowsLessonsBySubjectAndClass(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"}, "complete": {"on"}})
	request(t, server, http.MethodPost, "/agenda/2026-09-02/lessons/2",
		url.Values{"time": {lessonTimes[1]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Decimals"}})
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"9A"}, "topic": {"Algebra"}})
	request(t, server, http.MethodPost, "/agenda/2026-08-14/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"7A"}, "topic": {"Poetry"}})

	empty := request(t, server, http.MethodGet, "/review", nil)
	assertContains(t, empty, "Choose a subject")

	review := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil)
	assertContains(t, review, "Fractions")
	assertContains(t, review, "Decimals")
	assertContains(t, review, "2 lessons recorded")
	if strings.Contains(review, "Algebra") {
		t.Fatal("review leaked a lesson from a different class")
	}
	if strings.Contains(review, "Poetry") {
		t.Fatal("review leaked a lesson from a different subject")
	}

	// oldest first, so curriculum coverage reads chronologically
	if strings.Index(review, "Fractions") > strings.Index(review, "Decimals") {
		t.Fatal("review lessons are not ordered oldest first")
	}
}

func TestReviewIncludesTemplateOnlyLessons(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	// Setting up the weekly timetable alone (never opening any specific day's
	// agenda) should still surface those lessons in the yearly review.
	request(t, server, http.MethodPost, "/schedule",
		url.Values{"Tuesday-2-time": {lessonTimes[1]}, "Tuesday-2-subject": {"English"}, "Tuesday-2-class": {"7A"}})

	review := request(t, server, http.MethodGet, "/review?subject=English&class=7A&ready=0", nil)
	if strings.Contains(review, "No recorded lessons") {
		t.Fatal("review does not include lessons that only exist in the weekly timetable")
	}
	assertContains(t, review, "lessons recorded")
}

func TestReviewExpandsLessonInline(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{
			"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"},
			"phase_1_content": {"Explain equivalent fractions"},
		})

	review := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil)
	assertContains(t, review, "Explain equivalent fractions")
	assertContains(t, review, "Open in agenda to edit")
	if !strings.Contains(review, "<details") {
		t.Fatal("review rows should expand inline instead of only linking away")
	}
	assertContains(t, review, "id=\"toggle-all-lessons\"")
	assertContains(t, review, "id=\"review-list\"")
}

func TestReviewReadyOnlyFilter(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"}, "complete": {"on"}})
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Decimals"}})

	// ready to teach is the default when no explicit status is chosen
	byDefault := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A", nil)
	assertContains(t, byDefault, "Fractions")
	if strings.Contains(byDefault, "Decimals") {
		t.Fatal("review should default to showing only lessons ready to teach")
	}

	all := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil)
	assertContains(t, all, "Fractions")
	assertContains(t, all, "Decimals")

	readyOnly := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=1", nil)
	assertContains(t, readyOnly, "Fractions")
	if strings.Contains(readyOnly, "Decimals") {
		t.Fatal("ready-only filter should hide lessons that are not marked ready to teach")
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
