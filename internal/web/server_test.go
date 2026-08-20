package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAgendaInheritsTemplateAndSavesDailyOverride(t *testing.T) {
	dir := t.TempDir()
	handler := NewPersistentServer(dir)
	cookie := signUp(t, handler)

	dashboard := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, dashboard, "Wednesday, August 12, 2026")
	assertContains(t, dashboard, "Mathematics")
	assertContains(t, dashboard, "Learning objectives")
	assertContains(t, dashboard, "Lunch break")

	form := url.Values{
		"time": {"07:30-08:15"}, "subject": {"History"}, "class": {"8A"},
		"topic": {"Industrial Revolution"}, "phase_1_content": {"Identify three causes"},
		"phase_2_materials": {"Source cards"}, "phase_3_notes": {"Sentence starters"}, "complete": {"on"},
	}
	saved := request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", form, cookie)
	assertContains(t, saved, "History")
	assertContains(t, saved, "Industrial Revolution")
	assertContains(t, saved, "Ready")

	reloaded := request(t, NewPersistentServer(dir), http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, reloaded, "Identify three causes")
	assertContains(t, reloaded, "Source cards")
	assertContains(t, reloaded, "Sentence starters")
}

func TestYearAndTimetableViews(t *testing.T) {
	handler := NewServer()
	cookie := signUp(t, handler)
	year := request(t, handler, http.MethodGet, "/year", nil, cookie)
	assertContains(t, year, "School year 2026/2027")
	assertContains(t, year, "/?date=2027-07-02")

	schedule := request(t, handler, http.MethodGet, "/schedule", nil, cookie)
	assertContains(t, schedule, "Weekly timetable")
	assertContains(t, schedule, "Monday-1-time")
	assertContains(t, schedule, "Save timetable")
	assertContains(t, schedule, "Planner settings")
}

func TestAgendaSubjectFilterShowsWholeWeek(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

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
	request(t, server, http.MethodPost, "/schedule", form, cookie)

	weekly := request(t, server, http.MethodGet, "/?date=2026-08-12&subject=Mathematics", nil, cookie)
	assertContains(t, weekly, "Mathematics at a glance")
	assertContains(t, weekly, "Monday")
	assertContains(t, weekly, "Friday")
	assertContains(t, weekly, "Class 7A")
	assertContains(t, weekly, "Class 8B")
	if strings.Contains(weekly, "Class 9A") {
		t.Fatal("subject week contains an unrelated English lesson")
	}

	classWeek := request(t, server, http.MethodGet, "/?date=2026-08-12&subject=Mathematics&class=7A", nil, cookie)
	assertContains(t, classWeek, "Mathematics · 7A at a glance")
	assertContains(t, classWeek, "Class 7A")
	if strings.Contains(classWeek, "Class 8B") {
		t.Fatal("class filter contains a different Mathematics class")
	}

	classOnly := request(t, server, http.MethodGet, "/?date=2026-08-12&class=7A", nil, cookie)
	assertContains(t, classOnly, "All subjects · 7A at a glance")
	assertContains(t, classOnly, "Class 7A")
	if strings.Contains(classOnly, "Class 8B") || strings.Contains(classOnly, "Class 9A") {
		t.Fatal("class-only filter contains a lesson from another class")
	}
}

func TestDayAndWeekOverride(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	dashboard := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, dashboard, "Learning objectives")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"School camp"}, "notes": {"Bring hiking boots"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}}, cookie)

	overridden := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, overridden, "School camp")
	assertContains(t, overridden, "Bring hiking boots")
	assertContains(t, overridden, "Remove event")
	if strings.Contains(overridden, "Learning objectives") {
		t.Fatal("overridden day still shows regular lesson plan")
	}

	// A neighbouring day should be unaffected by a single-day override.
	tuesday := request(t, server, http.MethodGet, "/?date=2026-08-11", nil, cookie)
	assertContains(t, tuesday, "Learning objectives")

	// A second event on the same day should show alongside the first.
	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"Photo day"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}}, cookie)
	both := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, both, "School camp")
	assertContains(t, both, "Photo day")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/clear", nil, cookie)
	oneLeft := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	if strings.Contains(oneLeft, "Bring hiking boots") {
		t.Fatal("removed event still shown")
	}
	assertContains(t, oneLeft, "Photo day")

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/clear", nil, cookie)
	restored := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, restored, "Learning objectives")

	request(t, server, http.MethodPost, "/agenda/2026-08-10/override",
		url.Values{"title": {"Sports week"}, "start": {"2026-08-10"}, "end": {"2026-08-14"}}, cookie)

	for _, date := range []string{"2026-08-10", "2026-08-11", "2026-08-12", "2026-08-13", "2026-08-14"} {
		body := request(t, server, http.MethodGet, "/?date="+date, nil, cookie)
		assertContains(t, body, "Sports week")
	}
}

func TestDayOverrideActivities(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override", url.Values{
		"title": {"Field trip"}, "start": {"2026-08-12"}, "end": {"2026-08-12"},
		"activity_0_time": {"09:00"}, "activity_0_name": {"Museum visit"},
		"activity_0_material": {"Permission slips"}, "activity_0_description": {"Guided tour"},
		"activity_0_notes": {"Bring water bottles"},
		"activity_1_time":  {"12:00"}, "activity_1_name": {"Lunch"},
	}, cookie)

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, body, "Museum visit")
	assertContains(t, body, "Permission slips")
	assertContains(t, body, "Guided tour")
	assertContains(t, body, "Bring water bottles")
	assertContains(t, body, "Lunch")
}

func TestEditDayOverride(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override", url.Values{
		"title": {"Field trip"}, "notes": {"Meet at the gate"}, "start": {"2026-08-12"}, "end": {"2026-08-12"},
		"activity_0_time": {"09:00"}, "activity_0_name": {"Museum visit"},
	}, cookie)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override/0/edit", url.Values{
		"title": {"Field trip (updated)"}, "notes": {"Meet at the main gate"},
		"activity_0_time": {"10:00"}, "activity_0_name": {"Science center"},
	}, cookie)

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
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
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"Assembly"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}}, cookie)

	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, body, "Assembly")
	if strings.Contains(body, "data-activities=\"null\"") {
		t.Fatal("event with no activities rendered data-activities as null, which breaks JSON.parse in the edit button")
	}
	assertContains(t, body, "data-activities=\"[]\"")
}

func TestLessonNotesShowUpInLog(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	saved := request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Fire alarm interrupted the lesson"}}, cookie)
	assertContains(t, saved, "Fire alarm interrupted the lesson")

	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/2/notes",
		url.Values{"text": {"Great class discussion"}}, cookie)

	notes := request(t, server, http.MethodGet, "/notes", nil, cookie)
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
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Struggled with fractions"}, "student": {"Alex Doe"}}, cookie)
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/2/notes",
		url.Values{"text": {"Great class discussion"}}, cookie)

	all := request(t, server, http.MethodGet, "/notes", nil, cookie)
	assertContains(t, all, "Struggled with fractions")
	assertContains(t, all, "Alex Doe")
	assertContains(t, all, "Great class discussion")
	// the student never added to Settings should still appear as a filter option
	assertContains(t, all, "<option value=\"Alex Doe\"")

	filtered := request(t, server, http.MethodGet, "/notes?student=Alex+Doe", nil, cookie)
	assertContains(t, filtered, "Struggled with fractions")
	if strings.Contains(filtered, "Great class discussion") {
		t.Fatal("student filter should hide notes tagged for someone else (or untagged)")
	}
}

func TestResetDataWipesEverything(t *testing.T) {
	dir := t.TempDir()
	handler := NewPersistentServer(dir)
	cookie := signUp(t, handler)

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1/notes",
		url.Values{"text": {"Struggled with fractions"}, "student": {"Alex Doe"}}, cookie)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/override",
		url.Values{"title": {"School camp"}, "start": {"2026-08-12"}, "end": {"2026-08-12"}}, cookie)
	request(t, handler, http.MethodPost, "/settings",
		url.Values{"teacher": {"Ms. Weber"}, "students": {"Alex Doe"}}, cookie)

	request(t, handler, http.MethodPost, "/reset", nil, cookie)

	notes := request(t, handler, http.MethodGet, "/notes", nil, cookie)
	if strings.Contains(notes, "Struggled with fractions") {
		t.Fatal("reset should clear existing notes")
	}

	agenda := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	if strings.Contains(agenda, "day-override-list") {
		t.Fatal("reset should clear existing special day events")
	}

	// data should also be reset for anyone reloading from the persisted file
	reloaded := request(t, NewPersistentServer(dir), http.MethodGet, "/notes", nil, cookie)
	if strings.Contains(reloaded, "Struggled with fractions") {
		t.Fatal("reset should persist the wiped data to disk")
	}
}

func TestReviewShowsLessonsBySubjectAndClass(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"}, "complete": {"on"}}, cookie)
	request(t, server, http.MethodPost, "/agenda/2026-09-02/lessons/2",
		url.Values{"time": {lessonTimes[1]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Decimals"}}, cookie)
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"9A"}, "topic": {"Algebra"}}, cookie)
	request(t, server, http.MethodPost, "/agenda/2026-08-14/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"7A"}, "topic": {"Poetry"}}, cookie)

	empty := request(t, server, http.MethodGet, "/review", nil, cookie)
	assertContains(t, empty, "Choose a subject")

	review := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil, cookie)
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
	cookie := signUp(t, handler)

	// Setting up the weekly timetable alone (never opening any specific day's
	// agenda) should still surface those lessons in the yearly review.
	request(t, server, http.MethodPost, "/schedule",
		url.Values{"Tuesday-2-time": {lessonTimes[1]}, "Tuesday-2-subject": {"English"}, "Tuesday-2-class": {"7A"}}, cookie)

	review := request(t, server, http.MethodGet, "/review?subject=English&class=7A&ready=0", nil, cookie)
	if strings.Contains(review, "No recorded lessons") {
		t.Fatal("review does not include lessons that only exist in the weekly timetable")
	}
	assertContains(t, review, "lessons recorded")
}

func TestReviewExpandsLessonInline(t *testing.T) {
	handler := NewServer()
	server := handler.(*http.ServeMux)
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{
			"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"},
			"phase_1_content": {"Explain equivalent fractions"},
		}, cookie)

	review := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil, cookie)
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
	cookie := signUp(t, handler)

	request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions"}, "complete": {"on"}}, cookie)
	request(t, server, http.MethodPost, "/agenda/2026-08-13/lessons/1",
		url.Values{"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Decimals"}}, cookie)

	// ready to teach is the default when no explicit status is chosen
	byDefault := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A", nil, cookie)
	assertContains(t, byDefault, "Fractions")
	if strings.Contains(byDefault, "Decimals") {
		t.Fatal("review should default to showing only lessons ready to teach")
	}

	all := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=0", nil, cookie)
	assertContains(t, all, "Fractions")
	assertContains(t, all, "Decimals")

	readyOnly := request(t, server, http.MethodGet, "/review?subject=Mathematics&class=7A&ready=1", nil, cookie)
	assertContains(t, readyOnly, "Fractions")
	if strings.Contains(readyOnly, "Decimals") {
		t.Fatal("ready-only filter should hide lessons that are not marked ready to teach")
	}
}

func TestSignupValidation(t *testing.T) {
	handler := NewServer()

	shortPassword := postAuthForm(t, handler, "/signup", url.Values{
		"username": {"teacher"}, "password": {"short"}, "confirm": {"short"},
	})
	if shortPassword.Code != http.StatusOK {
		t.Fatalf("short password returned %d, want 200", shortPassword.Code)
	}
	assertContains(t, shortPassword.Body.String(), "password must be at least 8 characters")

	mismatched := postAuthForm(t, handler, "/signup", url.Values{
		"username": {"teacher"}, "password": {"password1234"}, "confirm": {"different1234"},
	})
	if mismatched.Code != http.StatusOK {
		t.Fatalf("mismatched password returned %d, want 200", mismatched.Code)
	}
	assertContains(t, mismatched.Body.String(), "Passwords do not match")

	_ = signUp(t, handler)
	duplicate := postAuthForm(t, handler, "/signup", url.Values{
		"username": {"TEACHER"}, "password": {"password1234"}, "confirm": {"password1234"},
	})
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate username returned %d, want 200", duplicate.Code)
	}
	assertContains(t, duplicate.Body.String(), "that username is already taken")
}

func TestLoginFailuresAndLogout(t *testing.T) {
	handler := NewServer()
	_ = signUp(t, handler)

	wrongPassword := postAuthForm(t, handler, "/login", url.Values{
		"username": {"teacher"}, "password": {"wrong-password"},
	})
	if wrongPassword.Code != http.StatusOK {
		t.Fatalf("wrong password returned %d, want 200", wrongPassword.Code)
	}
	assertContains(t, wrongPassword.Body.String(), "Invalid username or password")

	unknownUser := postAuthForm(t, handler, "/login", url.Values{
		"username": {"unknown"}, "password": {"password1234"},
	})
	if unknownUser.Code != http.StatusOK {
		t.Fatalf("unknown username returned %d, want 200", unknownUser.Code)
	}
	assertContains(t, unknownUser.Body.String(), "Invalid username or password")

	logoutHandler := NewServer()
	cookie := signUp(t, logoutHandler)
	logout := requestWithResponse(t, logoutHandler, http.MethodPost, "/logout", nil, cookie)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/login" {
		t.Fatalf("logout returned %d with location %q", logout.Code, logout.Header().Get("Location"))
	}
	if expired := logout.Result().Cookies(); len(expired) == 0 || expired[0].MaxAge != -1 {
		t.Fatal("logout did not expire the session cookie")
	}
	protected := requestWithResponse(t, logoutHandler, http.MethodGet, "/", nil, "")
	if protected.Code != http.StatusSeeOther || protected.Header().Get("Location") != "/login" {
		t.Fatalf("logged-out request returned %d with location %q", protected.Code, protected.Header().Get("Location"))
	}
}

func TestAccountsHaveIsolatedPlannerData(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Fractions for A"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"8B"}, "topic": {"Poetry for B"},
	}, accountB)

	bodyA := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountA)
	assertContains(t, bodyA, "Fractions for A")
	if strings.Contains(bodyA, "Poetry for B") {
		t.Fatal("account A can see account B's lesson")
	}

	bodyB := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountB)
	assertContains(t, bodyB, "Poetry for B")
	if strings.Contains(bodyB, "Fractions for A") {
		t.Fatal("account B can see account A's lesson")
	}
}

func TestResetOnlyAffectsCurrentAccount(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"A private lesson"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"8B"}, "topic": {"B private lesson"},
	}, accountB)

	request(t, handler, http.MethodPost, "/reset", nil, accountA)

	bodyA := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountA)
	if strings.Contains(bodyA, "A private lesson") {
		t.Fatal("reset did not clear the current account's lesson")
	}

	bodyB := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountB)
	assertContains(t, bodyB, "B private lesson")
}

func TestBackupContainsOnlyCurrentAccountData(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"A backup lesson"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"8B"}, "topic": {"B backup lesson"},
	}, accountB)

	backup := requestWithResponse(t, handler, http.MethodGet, "/backup", nil, accountA)
	if backup.Code != http.StatusOK {
		t.Fatalf("backup returned %d: %.300s", backup.Code, backup.Body.String())
	}
	if got := backup.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("backup content type = %q", got)
	}
	if got := backup.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment; filename=lehrerin-backup-") {
		t.Fatalf("backup content disposition = %q", got)
	}
	assertContains(t, backup.Body.String(), "A backup lesson")
	if strings.Contains(backup.Body.String(), "B backup lesson") {
		t.Fatal("backup contains another account's lesson")
	}
}

func TestRestoreReplacesOnlyCurrentAccountData(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"Original A lesson"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"8B"}, "topic": {"B lesson stays"},
	}, accountB)

	backup := requestWithResponse(t, handler, http.MethodGet, "/backup", nil, accountA)
	restored := storeData{}
	if err := json.Unmarshal(backup.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	restored.Agendas["2026-08-12"][1] = restored.Agendas["2026-08-12"][1]
	lesson := restored.Agendas["2026-08-12"][1]
	lesson.Slot.Topic = "Restored A lesson"
	restored.Agendas["2026-08-12"][1] = lesson
	data, err := json.Marshal(restored)
	if err != nil {
		t.Fatalf("encode modified backup: %v", err)
	}

	restore := multipartRequest(t, handler, "/restore", data, accountA)
	if restore.Code != http.StatusNoContent || restore.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("restore returned %d with redirect %q", restore.Code, restore.Header().Get("HX-Redirect"))
	}
	bodyA := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountA)
	assertContains(t, bodyA, "Restored A lesson")
	bodyB := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountB)
	assertContains(t, bodyB, "B lesson stays")
}

func TestRestoreRejectsInvalidBackup(t *testing.T) {
	handler := NewServer()
	cookie := signUp(t, handler)
	response := multipartRequest(t, handler, "/restore", []byte(`{"not":"a planner"}`), cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid restore returned %d, want 400", response.Code)
	}
}

func TestDownloadDayIncludesDetailedPlan(t *testing.T) {
	handler := NewServer()
	cookie := signUp(t, handler)
	request(t, handler, http.MethodPost, "/agenda/2026-08-20/lessons/1", url.Values{
		"time": {"07:30-08:15"}, "subject": {"Biology"}, "class": {"8C"}, "topic": {"Fair experiments"},
		"phase_1_content": {"Design the investigation"}, "phase_1_materials": {"Planning sheet"},
		"phase_1_notes": {"Check control variables"},
	}, cookie)

	response := requestWithResponse(t, handler, http.MethodGet, "/download/day?date=2026-08-20", nil, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("day download returned %d", response.Code)
	}
	if got := response.Header().Get("Content-Disposition"); got != "attachment; filename=lehrerin-day-2026-08-20.html" {
		t.Fatalf("day download disposition = %q", got)
	}
	assertContains(t, response.Body.String(), "Fair experiments")
	assertContains(t, response.Body.String(), "Planning sheet")
	assertContains(t, response.Body.String(), "Check control variables")
}

func TestDownloadWeekUsesSelectedDateAndCurrentAccount(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")
	request(t, handler, http.MethodPost, "/agenda/2026-08-19/lessons/1", url.Values{
		"time": {"07:30-08:15"}, "subject": {"Mathematics"}, "class": {"7B"}, "topic": {"A weekly plan"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-19/lessons/1", url.Values{
		"time": {"07:30-08:15"}, "subject": {"English"}, "class": {"9A"}, "topic": {"B private plan"},
	}, accountB)

	response := requestWithResponse(t, handler, http.MethodGet, "/download/week?date=2026-08-20", nil, accountA)
	if response.Code != http.StatusOK {
		t.Fatalf("week download returned %d", response.Code)
	}
	if got := response.Header().Get("Content-Disposition"); got != "attachment; filename=lehrerin-week-2026-08-17.html" {
		t.Fatalf("week download disposition = %q", got)
	}
	assertContains(t, response.Body.String(), "A weekly plan")
	if strings.Contains(response.Body.String(), "B private plan") {
		t.Fatal("week download contains another account's lesson")
	}
}

func TestUndoRestoresPreviousLessonAndCapsAt100Entries(t *testing.T) {
	handler := NewServer()
	cookie := signUp(t, handler)
	server := handler.(*http.ServeMux)

	for index := 0; index < 101; index++ {
		request(t, server, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
			"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {fmt.Sprintf("Revision %d", index)},
		}, cookie)
	}

	assertUndoRedirect(t, server, cookie)
	body := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, body, "Revision 99")
	if strings.Contains(body, "Revision 100") {
		t.Fatal("undo did not restore the previous lesson")
	}

	for index := 0; index < 99; index++ {
		assertUndoRedirect(t, server, cookie)
	}
	oldest := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, oldest, "Revision 0")
	assertEmptyUndoRedirect(t, server, cookie)
	stillOldest := request(t, server, http.MethodGet, "/?date=2026-08-12", nil, cookie)
	assertContains(t, stillOldest, "Revision 0")
}

func TestUndoHistoryIsIsolatedPerAccount(t *testing.T) {
	handler := NewServer()
	accountA := signUpAs(t, handler, "teacher-a", "password1234")
	accountB := signUpAs(t, handler, "teacher-b", "password1234")

	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"Mathematics"}, "class": {"7A"}, "topic": {"A lesson"},
	}, accountA)
	request(t, handler, http.MethodPost, "/agenda/2026-08-12/lessons/1", url.Values{
		"time": {lessonTimes[0]}, "subject": {"English"}, "class": {"8B"}, "topic": {"B lesson"},
	}, accountB)

	assertUndoRedirect(t, handler, accountA)
	bodyA := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountA)
	if strings.Contains(bodyA, "A lesson") {
		t.Fatal("account A undo did not restore its previous state")
	}
	bodyB := request(t, handler, http.MethodGet, "/?date=2026-08-12", nil, accountB)
	assertContains(t, bodyB, "B lesson")
}

func assertUndoRedirect(t *testing.T, handler http.Handler, cookie string) {
	t.Helper()
	response := requestWithResponse(t, handler, http.MethodPost, "/undo", url.Values{"date": {"2026-08-12"}}, cookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/?date=2026-08-12" {
		t.Fatalf("undo returned %d with location %q", response.Code, response.Header().Get("Location"))
	}
}

func assertEmptyUndoRedirect(t *testing.T, handler http.Handler, cookie string) {
	t.Helper()
	response := requestWithResponse(t, handler, http.MethodPost, "/undo", url.Values{"date": {"2026-08-12"}}, cookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/?date=2026-08-12&undo=empty" {
		t.Fatalf("empty undo returned %d with location %q", response.Code, response.Header().Get("Location"))
	}
}

func multipartRequest(t *testing.T, handler http.Handler, target string, data []byte, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("backup", "lehrerin-backup.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func postAuthForm(t *testing.T, handler http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithResponse(t, handler, http.MethodPost, target, form, "")
}

func signUpAs(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()
	resp := postAuthForm(t, handler, "/signup", url.Values{
		"username": {username}, "password": {password}, "confirm": {password},
	})
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("signup for %q returned %d: %.300s", username, resp.Code, resp.Body.String())
	}
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie.Name + "=" + cookie.Value
		}
	}
	t.Fatalf("signup for %q did not set a session cookie", username)
	return ""
}

func signUp(t *testing.T, handler http.Handler) string {
	t.Helper()
	form := url.Values{"username": {"teacher"}, "password": {"password1234"}, "confirm": {"password1234"}}
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("signup returned %d: %.300s", resp.Code, resp.Body.String())
	}
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie.Name + "=" + cookie.Value
		}
	}
	t.Fatal("signup did not set a session cookie")
	return ""
}

func request(t *testing.T, handler http.Handler, method, target string, form url.Values, cookie string) string {
	t.Helper()
	response := requestWithResponse(t, handler, method, target, form, cookie)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("%s %s returned %d: %.300s", method, target, response.Code, response.Body.String())
	}
	return response.Body.String()
}

func requestWithResponse(t *testing.T, handler http.Handler, method, target string, form url.Values, cookie string) *httptest.ResponseRecorder {
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
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertContains(t *testing.T, body, expected string) {
	t.Helper()
	if !strings.Contains(body, expected) {
		t.Fatalf("response does not contain %q", expected)
	}
}
