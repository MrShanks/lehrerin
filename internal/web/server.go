package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

const dateLayout = "2006-01-02"

var lessonTimes = []string{
	"07:30-08:15", "08:20-09:05", "09:10-09:55", "10:25-11:10", "11:15-12:00",
	"13:45-14:30", "14:35-15:20", "15:30-16:15", "16:15-17:00",
}

type Slot struct {
	Number  int    `json:"number"`
	Time    string `json:"time"`
	Subject string `json:"subject"`
	Class   string `json:"class"`
	Topic   string `json:"topic"`
}

type Phase struct {
	Name      string `json:"name"`
	Duration  string `json:"duration"`
	Content   string `json:"content"`
	Materials string `json:"materials"`
	Notes     string `json:"notes"`
}

type LessonNote struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

type Lesson struct {
	Slot     Slot         `json:"slot"`
	Phases   []Phase      `json:"phases"`
	Complete bool         `json:"complete"`
	Notes    []LessonNote `json:"notes"`
}

type Activity struct {
	Time        string `json:"time"`
	Name        string `json:"name"`
	Material    string `json:"material"`
	Description string `json:"description"`
	Notes       string `json:"notes"`
}

type DayOverride struct {
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	Activities []Activity `json:"activities"`
}

type storeData struct {
	Teacher      string                    `json:"teacher"`
	School       string                    `json:"school"`
	Subjects     []string                  `json:"subjects"`
	Classes      []string                  `json:"classes"`
	Schedule     map[string][]Slot         `json:"schedule"`
	Agendas      map[string]map[int]Lesson `json:"agendas"`
	DayOverrides map[string][]DayOverride  `json:"dayOverrides"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data storeData
}

type dayLink struct {
	Date       string
	Day        string
	Number     string
	Selected   bool
	Today      bool
	Overridden bool
	EventCount int
}

type weekRow struct {
	Number int
	Days   []dayLink
}

type subjectDay struct {
	Date        string
	Day         string
	Number      string
	Lessons     []Lesson
	LessonCount int
}

type pageData struct {
	View          string
	Teacher       string
	School        string
	Date          string
	DateInput     string
	PrevDate      string
	NextDate      string
	Week          []dayLink
	Weeks         []weekRow
	Lessons       []Lesson
	Schedule      map[string][]Slot
	Weekdays      []string
	Subjects      []string
	Classes       []string
	DoneCount     int
	TotalCount    int
	SubjectFilter string
	SubjectWeek   []subjectDay
	SubjectCount  int
	SchoolYear    string
	YearStart     string
	YearEnd       string
	Overridden    bool
	Overrides     []overrideView
	Notes         []noteView
}

type lessonData struct {
	Date     string
	Lesson   Lesson
	Subjects []string
	Classes  []string
}

type overrideView struct {
	Index      int
	Title      string
	Notes      string
	Activities []Activity
}

type noteView struct {
	Date       string
	DateLabel  string
	Time       string
	Text       string
	Subject    string
	Class      string
	Topic      string
	SlotNumber int
}

type scheduleData struct {
	Weekday string
	Slot    Slot
}

type Server struct {
	templates *template.Template
	store     *Store
}

func NewServer() http.Handler {
	return newServer("")
}

func NewPersistentServer(path string) http.Handler {
	return newServer(path)
}

func newServer(path string) http.Handler {
	functions := template.FuncMap{
		"lessonData": func(date string, lesson Lesson, subjects, classes []string) lessonData {
			return lessonData{Date: date, Lesson: lesson, Subjects: subjects, Classes: classes}
		},
		"scheduleData": func(weekday string, slot Slot) scheduleData {
			return scheduleData{Weekday: weekday, Slot: slot}
		},
		"toJSON": func(value any) (string, error) {
			bytes, err := json.Marshal(value)
			if err != nil {
				return "", err
			}
			return string(bytes), nil
		},
	}
	templates := template.Must(template.New("").Funcs(functions).ParseFS(assets, "templates/*.html"))
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}

	server := &Server{templates: templates, store: newStore(path)}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /", server.agenda)
	mux.HandleFunc("GET /year", server.year)
	mux.HandleFunc("GET /schedule", server.schedule)
	mux.HandleFunc("GET /notes", server.notes)
	mux.HandleFunc("POST /agenda/{date}/lessons/{slot}", server.saveLesson)
	mux.HandleFunc("POST /agenda/{date}/lessons/{slot}/notes", server.addLessonNote)
	mux.HandleFunc("POST /agenda/{date}/override", server.saveDayOverride)
	mux.HandleFunc("POST /agenda/{date}/override/{index}/edit", server.updateDayOverride)
	mux.HandleFunc("POST /agenda/{date}/override/{index}/clear", server.clearDayOverride)
	mux.HandleFunc("POST /schedule", server.saveSchedule)
	mux.HandleFunc("POST /settings", server.saveSettings)
	return mux
}

func newStore(path string) *Store {
	store := &Store{path: path, data: defaultStoreData()}
	if path == "" {
		return store
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(contents, &store.data)
	}
	for key, overrides := range store.data.DayOverrides {
		if len(overrides) == 0 {
			delete(store.data.DayOverrides, key)
		}
	}
	return store
}

func defaultStoreData() storeData {
	data := storeData{
		Teacher:      "Ms. Weber",
		School:       "North Community School",
		Subjects:     []string{"Mathematics", "English", "Biology", "Science", "History", "Geography", "Music", "Art", "Physical Education"},
		Classes:      []string{"7A", "7B", "7C", "8A", "8B", "8C", "9A", "9B", "9C", "9D"},
		Schedule:     make(map[string][]Slot),
		Agendas:      make(map[string]map[int]Lesson),
		DayOverrides: make(map[string][]DayOverride),
	}
	for _, weekday := range weekdays() {
		data.Schedule[weekday] = blankSlots()
	}
	data.Schedule["Wednesday"][0].Subject = "Mathematics"
	data.Schedule["Wednesday"][0].Class = "7B"
	data.Schedule["Wednesday"][1].Subject = "English"
	data.Schedule["Wednesday"][1].Class = "9A"
	data.Schedule["Wednesday"][5].Subject = "Biology"
	data.Schedule["Wednesday"][5].Class = "8C"
	return data
}

func blankSlots() []Slot {
	slots := make([]Slot, len(lessonTimes))
	for index, lessonTime := range lessonTimes {
		slots[index] = Slot{Number: index + 1, Time: lessonTime}
	}
	return slots
}

func weekdays() []string {
	return []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday"}
}

func phases() []Phase {
	return []Phase{
		{Name: "Admin"},
		{Name: "Learning objectives"},
		{Name: "Starter"},
		{Name: "Tasks and development"},
		{Name: "Closing and assessment"},
	}
}

func (s *Store) agenda(date time.Time) []Lesson {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agendaLocked(date)
}

// agendaLocked rebuilds the day from the current schedule template on every
// call, overlaying any per-lesson edits saved for that specific date. This
// keeps slots the teacher hasn't edited in sync with later timetable changes.
func (s *Store) agendaLocked(date time.Time) []Lesson {
	key := date.Format(dateLayout)
	if len(s.data.DayOverrides[key]) > 0 {
		return nil
	}
	overrides := s.data.Agendas[key]
	templateSlots := s.data.Schedule[date.Weekday().String()]
	if len(templateSlots) == 0 {
		templateSlots = blankSlots()
	}
	lessons := make([]Lesson, len(templateSlots))
	for index, slot := range templateSlots {
		if override, ok := overrides[index]; ok {
			lessons[index] = cloneLesson(override)
			continue
		}
		lessons[index] = Lesson{Slot: slot, Phases: phases()}
	}
	return lessons
}

func cloneLesson(lesson Lesson) Lesson {
	lesson.Phases = append([]Phase(nil), lesson.Phases...)
	return lesson
}

func (s *Store) saveLessonOverride(date time.Time, slotIndex int, lesson Lesson) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := date.Format(dateLayout)
	if s.data.Agendas[key] == nil {
		s.data.Agendas[key] = make(map[int]Lesson)
	}
	s.data.Agendas[key][slotIndex] = cloneLesson(lesson)
	return s.persistLocked()
}

// allNotes collects every lesson note across all days, most recent first.
func (s *Store) allNotes() []noteView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var notes []noteView
	for dateKey, lessons := range s.data.Agendas {
		date, err := time.Parse(dateLayout, dateKey)
		if err != nil {
			continue
		}
		for _, lesson := range lessons {
			for _, note := range lesson.Notes {
				notes = append(notes, noteView{
					Date: dateKey, DateLabel: date.Format("Monday, January 2, 2006"),
					Time: note.Time, Text: note.Text,
					Subject: lesson.Slot.Subject, Class: lesson.Slot.Class, Topic: lesson.Slot.Topic,
					SlotNumber: lesson.Slot.Number,
				})
			}
		}
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].Date != notes[j].Date {
			return notes[i].Date > notes[j].Date
		}
		if notes[i].Time != notes[j].Time {
			return notes[i].Time > notes[j].Time
		}
		return notes[i].SlotNumber < notes[j].SlotNumber
	})
	return notes
}

func (s *Store) dayOverrides(date time.Time) []DayOverride {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]DayOverride(nil), s.data.DayOverrides[date.Format(dateLayout)]...)
}

func (s *Store) addDayOverride(dates []time.Time, override DayOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, date := range dates {
		key := date.Format(dateLayout)
		s.data.DayOverrides[key] = append(s.data.DayOverrides[key], override)
	}
	return s.persistLocked()
}

func (s *Store) updateDayOverride(date time.Time, index int, override DayOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := date.Format(dateLayout)
	overrides := s.data.DayOverrides[key]
	if index < 0 || index >= len(overrides) {
		return errors.New("event index out of range")
	}
	overrides[index] = override
	return s.persistLocked()
}

func (s *Store) removeDayOverride(date time.Time, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := date.Format(dateLayout)
	overrides := s.data.DayOverrides[key]
	if index < 0 || index >= len(overrides) {
		return errors.New("event index out of range")
	}
	overrides = append(overrides[:index], overrides[index+1:]...)
	if len(overrides) == 0 {
		delete(s.data.DayOverrides, key)
	} else {
		s.data.DayOverrides[key] = overrides
	}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, contents, 0o600)
}

func (s *Server) agenda(w http.ResponseWriter, r *http.Request) {
	date := requestedDate(r.URL.Query().Get("date"))
	s.render(w, "layout", s.agendaData(date, strings.TrimSpace(r.URL.Query().Get("subject"))))
}

func (s *Server) year(w http.ResponseWriter, _ *http.Request) {
	data := s.baseData("year")
	s.store.mu.RLock()
	eventCounts := make(map[string]int, len(s.store.data.DayOverrides))
	for key, overrides := range s.store.data.DayOverrides {
		eventCounts[key] = len(overrides)
	}
	s.store.mu.RUnlock()
	data.Weeks = schoolWeeks(eventCounts)
	s.render(w, "layout", data)
}

func (s *Server) notes(w http.ResponseWriter, _ *http.Request) {
	data := s.baseData("notes")
	data.Notes = s.store.allNotes()
	s.render(w, "layout", data)
}

func (s *Server) schedule(w http.ResponseWriter, _ *http.Request) {
	data := s.baseData("schedule")
	s.store.mu.RLock()
	data.Schedule = cloneSchedule(s.store.data.Schedule)
	s.store.mu.RUnlock()
	s.render(w, "layout", data)
}

func (s *Server) agendaData(date time.Time, subject string) pageData {
	data := s.baseData("agenda")
	data.Date = date.Format("Monday, January 2, 2006")
	data.DateInput = date.Format(dateLayout)
	if subject == "" {
		data.PrevDate = previousWeekday(date).Format(dateLayout)
		data.NextDate = nextWeekday(date).Format(dateLayout)
	} else {
		data.PrevDate = date.AddDate(0, 0, -7).Format(dateLayout)
		data.NextDate = date.AddDate(0, 0, 7).Format(dateLayout)
	}
	data.Week = weekLinks(date)
	data.SubjectFilter = subject
	if subject != "" {
		data.SubjectWeek, data.SubjectCount = s.subjectWeek(date, subject)
	}
	if overrides := s.store.dayOverrides(date); len(overrides) > 0 {
		data.Overridden = true
		data.Overrides = make([]overrideView, len(overrides))
		for index, override := range overrides {
			activities := override.Activities
			if activities == nil {
				activities = []Activity{}
			}
			data.Overrides[index] = overrideView{Index: index, Title: override.Title, Notes: override.Notes, Activities: activities}
		}
	}
	data.Lessons = s.store.agenda(date)
	data.TotalCount = len(data.Lessons)
	for _, lesson := range data.Lessons {
		if lesson.Complete {
			data.DoneCount++
		}
	}
	return data
}

func (s *Server) subjectWeek(selected time.Time, subject string) ([]subjectDay, int) {
	start := selected.AddDate(0, 0, -weekdayOffset(selected.Weekday()))
	days := make([]subjectDay, 5)
	total := 0
	for index := range days {
		date := start.AddDate(0, 0, index)
		var matches []Lesson
		for _, lesson := range s.store.agenda(date) {
			if strings.EqualFold(lesson.Slot.Subject, subject) {
				matches = append(matches, lesson)
			}
		}
		days[index] = subjectDay{
			Date: date.Format(dateLayout), Day: date.Format("Monday"), Number: date.Format("Jan 2"),
			Lessons: matches, LessonCount: len(matches),
		}
		total += len(matches)
	}
	return days, total
}

func (s *Server) baseData(view string) pageData {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	return pageData{
		View: view, Teacher: s.store.data.Teacher, School: s.store.data.School,
		Subjects: append([]string(nil), s.store.data.Subjects...), Classes: append([]string(nil), s.store.data.Classes...),
		Weekdays: weekdays(), SchoolYear: "2026/2027", YearStart: "August 10, 2026", YearEnd: "July 2, 2027",
	}
}

func (s *Server) saveLesson(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(dateLayout, r.PathValue("date"))
	if err != nil || date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		http.Error(w, "Invalid school date", http.StatusBadRequest)
		return
	}
	slotIndex, err := pathIndex(r.PathValue("slot"), len(lessonTimes))
	if err != nil {
		http.Error(w, "Invalid lesson", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	lessons := s.store.agenda(date)
	lesson := lessons[slotIndex]
	lesson.Slot.Time = strings.TrimSpace(r.FormValue("time"))
	lesson.Slot.Subject = strings.TrimSpace(r.FormValue("subject"))
	lesson.Slot.Class = strings.TrimSpace(r.FormValue("class"))
	lesson.Slot.Topic = strings.TrimSpace(r.FormValue("topic"))
	lesson.Complete = r.FormValue("complete") == "on"
	for index := range lesson.Phases {
		lesson.Phases[index].Duration = strings.TrimSpace(r.FormValue(fmt.Sprintf("phase_%d_duration", index)))
		lesson.Phases[index].Content = strings.TrimSpace(r.FormValue(fmt.Sprintf("phase_%d_content", index)))
		lesson.Phases[index].Materials = strings.TrimSpace(r.FormValue(fmt.Sprintf("phase_%d_materials", index)))
		lesson.Phases[index].Notes = strings.TrimSpace(r.FormValue(fmt.Sprintf("phase_%d_notes", index)))
	}
	if err := s.store.saveLessonOverride(date, slotIndex, lesson); err != nil {
		http.Error(w, "Could not save lesson", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "lessonSaved")
	s.store.mu.RLock()
	data := lessonData{
		Date: date.Format(dateLayout), Lesson: lesson,
		Subjects: append([]string(nil), s.store.data.Subjects...),
		Classes:  append([]string(nil), s.store.data.Classes...),
	}
	s.store.mu.RUnlock()
	s.render(w, "lesson-card", data)
}

func (s *Server) addLessonNote(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(dateLayout, r.PathValue("date"))
	if err != nil || date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		http.Error(w, "Invalid school date", http.StatusBadRequest)
		return
	}
	slotIndex, err := pathIndex(r.PathValue("slot"), len(lessonTimes))
	if err != nil {
		http.Error(w, "Invalid lesson", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		http.Error(w, "Note text is required", http.StatusBadRequest)
		return
	}

	lessons := s.store.agenda(date)
	lesson := lessons[slotIndex]
	lesson.Notes = append(lesson.Notes, LessonNote{Time: time.Now().Format("15:04"), Text: text})
	if err := s.store.saveLessonOverride(date, slotIndex, lesson); err != nil {
		http.Error(w, "Could not save note", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "lessonSaved")
	s.store.mu.RLock()
	data := lessonData{
		Date: date.Format(dateLayout), Lesson: lesson,
		Subjects: append([]string(nil), s.store.data.Subjects...),
		Classes:  append([]string(nil), s.store.data.Classes...),
	}
	s.store.mu.RUnlock()
	s.render(w, "lesson-card", data)
}

func (s *Server) saveDayOverride(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(dateLayout, r.PathValue("date"))
	if err != nil {
		http.Error(w, "Invalid school date", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "Event title is required", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(dateLayout, r.FormValue("start"))
	if err != nil {
		http.Error(w, "Invalid start date", http.StatusBadRequest)
		return
	}
	end := start
	if raw := strings.TrimSpace(r.FormValue("end")); raw != "" {
		end, err = time.Parse(dateLayout, raw)
		if err != nil {
			http.Error(w, "Invalid end date", http.StatusBadRequest)
			return
		}
	}
	if end.Before(start) {
		http.Error(w, "End date must be on or after the start date", http.StatusBadRequest)
		return
	}
	override := DayOverride{
		Title:      title,
		Notes:      strings.TrimSpace(r.FormValue("notes")),
		Activities: parseActivities(r),
	}
	if err := s.store.addDayOverride(schoolDatesInRange(start, end), override); err != nil {
		http.Error(w, "Could not save event", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/?date="+date.Format(dateLayout))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateDayOverride(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(dateLayout, r.PathValue("date"))
	if err != nil {
		http.Error(w, "Invalid school date", http.StatusBadRequest)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.Error(w, "Invalid event", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "Event title is required", http.StatusBadRequest)
		return
	}
	override := DayOverride{
		Title:      title,
		Notes:      strings.TrimSpace(r.FormValue("notes")),
		Activities: parseActivities(r),
	}
	if err := s.store.updateDayOverride(date, index, override); err != nil {
		http.Error(w, "Could not update event", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/?date="+date.Format(dateLayout))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearDayOverride(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(dateLayout, r.PathValue("date"))
	if err != nil {
		http.Error(w, "Invalid school date", http.StatusBadRequest)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.Error(w, "Invalid event", http.StatusBadRequest)
		return
	}
	if err := s.store.removeDayOverride(date, index); err != nil {
		http.Error(w, "Could not restore lessons", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/?date="+date.Format(dateLayout))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	s.store.mu.Lock()
	for _, weekday := range weekdays() {
		slots := s.store.data.Schedule[weekday]
		for index := range slots {
			prefix := fmt.Sprintf("%s-%d-", weekday, slots[index].Number)
			slots[index].Time = strings.TrimSpace(r.FormValue(prefix + "time"))
			slots[index].Subject = strings.TrimSpace(r.FormValue(prefix + "subject"))
			slots[index].Class = strings.TrimSpace(r.FormValue(prefix + "class"))
			slots[index].Topic = strings.TrimSpace(r.FormValue(prefix + "topic"))
		}
	}
	err := s.store.persistLocked()
	s.store.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not save timetable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "lessonSaved")
	data := s.baseData("schedule")
	s.store.mu.RLock()
	data.Schedule = cloneSchedule(s.store.data.Schedule)
	s.store.mu.RUnlock()
	s.render(w, "schedule-tabs", data)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	s.store.mu.Lock()
	s.store.data.Teacher = strings.TrimSpace(r.FormValue("teacher"))
	s.store.data.School = strings.TrimSpace(r.FormValue("school"))
	s.store.data.Subjects = splitLines(r.FormValue("subjects"))
	s.store.data.Classes = splitLines(r.FormValue("classes"))
	err := s.store.persistLocked()
	s.store.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not save settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/schedule")
	w.WriteHeader(http.StatusNoContent)
}

func requestedDate(raw string) time.Time {
	if date, err := time.Parse(dateLayout, raw); err == nil {
		return date
	}
	date := time.Now()
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

func weekLinks(selected time.Time) []dayLink {
	start := selected.AddDate(0, 0, -weekdayOffset(selected.Weekday()))
	links := make([]dayLink, 5)
	today := time.Now().Format(dateLayout)
	for index := range links {
		date := start.AddDate(0, 0, index)
		key := date.Format(dateLayout)
		links[index] = dayLink{Date: key, Day: date.Format("Mon"), Number: date.Format("2"), Selected: key == selected.Format(dateLayout), Today: key == today}
	}
	return links
}

func schoolWeeks(eventCounts map[string]int) []weekRow {
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.Local)
	end := time.Date(2027, time.July, 2, 0, 0, 0, 0, time.Local)
	var weeks []weekRow
	for monday := start; !monday.After(end); monday = monday.AddDate(0, 0, 7) {
		_, number := monday.ISOWeek()
		row := weekRow{Number: number}
		for day := 0; day < 5; day++ {
			date := monday.AddDate(0, 0, day)
			key := date.Format(dateLayout)
			count := eventCounts[key]
			row.Days = append(row.Days, dayLink{Date: key, Day: date.Format("Monday"), Number: date.Format("Jan 2"), Overridden: count > 0, EventCount: count})
		}
		weeks = append(weeks, row)
	}
	return weeks
}

func previousWeekday(date time.Time) time.Time {
	date = date.AddDate(0, 0, -1)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, -1)
	}
	return date
}

func nextWeekday(date time.Time) time.Time {
	date = date.AddDate(0, 0, 1)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

func weekdayOffset(day time.Weekday) int {
	if day == time.Sunday {
		return 6
	}
	return int(day - time.Monday)
}

func schoolDatesInRange(start, end time.Time) []time.Time {
	var dates []time.Time
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		dates = append(dates, date)
	}
	return dates
}

// parseActivities reads activity_{index}_{field} inputs added dynamically by the client.
func parseActivities(r *http.Request) []Activity {
	indices := make(map[int]bool)
	for key := range r.PostForm {
		rest, ok := strings.CutPrefix(key, "activity_")
		if !ok {
			continue
		}
		parts := strings.SplitN(rest, "_", 2)
		if len(parts) != 2 {
			continue
		}
		if index, err := strconv.Atoi(parts[0]); err == nil {
			indices[index] = true
		}
	}
	sortedIndices := make([]int, 0, len(indices))
	for index := range indices {
		sortedIndices = append(sortedIndices, index)
	}
	sort.Ints(sortedIndices)

	activities := make([]Activity, 0, len(sortedIndices))
	for _, index := range sortedIndices {
		prefix := fmt.Sprintf("activity_%d_", index)
		activity := Activity{
			Time:        strings.TrimSpace(r.FormValue(prefix + "time")),
			Name:        strings.TrimSpace(r.FormValue(prefix + "name")),
			Material:    strings.TrimSpace(r.FormValue(prefix + "material")),
			Description: strings.TrimSpace(r.FormValue(prefix + "description")),
			Notes:       strings.TrimSpace(r.FormValue(prefix + "notes")),
		}
		if activity == (Activity{}) {
			continue
		}
		activities = append(activities, activity)
	}
	return activities
}

func pathIndex(raw string, length int) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 || number > length {
		return 0, errors.New("index out of range")
	}
	return number - 1, nil
}

func cloneSchedule(schedule map[string][]Slot) map[string][]Slot {
	cloned := make(map[string][]Slot, len(schedule))
	for weekday, slots := range schedule {
		cloned[weekday] = append([]Slot(nil), slots...)
	}
	return cloned
}

func splitLines(value string) []string {
	var values []string
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, fmt.Sprintf("Template could not be rendered: %v", err), http.StatusInternalServerError)
	}
}
