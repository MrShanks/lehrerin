package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
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

var (
	schoolYearStart = time.Date(2026, time.August, 10, 0, 0, 0, 0, time.Local)
	schoolYearEnd   = time.Date(2027, time.July, 2, 0, 0, 0, 0, time.Local)
)

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
	Time    string `json:"time"`
	Text    string `json:"text"`
	Student string `json:"student"`
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
	Students     []string                  `json:"students"`
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
	View            string
	Teacher         string
	School          string
	Date            string
	DateInput       string
	PrevDate        string
	NextDate        string
	Week            []dayLink
	Weeks           []weekRow
	Lessons         []Lesson
	Schedule        map[string][]Slot
	Weekdays        []string
	Subjects        []string
	Classes         []string
	Students        []string
	DoneCount       int
	TotalCount      int
	SubjectFilter   string
	SubjectWeek     []subjectDay
	SubjectCount    int
	SchoolYear      string
	YearStart       string
	YearEnd         string
	Overridden      bool
	Overrides       []overrideView
	Notes           []noteView
	NoteStudent     string
	NoteStudents    []string
	ReviewSubject   string
	ReviewClass     string
	ReviewReadyOnly bool
	ReviewLessons   []reviewEntry
	ReviewCount     int
}

type lessonData struct {
	Date     string
	Lesson   Lesson
	Subjects []string
	Classes  []string
	Students []string
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
	Student    string
	SlotNumber int
}

type reviewEntry struct {
	Date       string
	DateLabel  string
	Time       string
	Subject    string
	Class      string
	Topic      string
	Complete   bool
	SlotNumber int
	Lesson     Lesson
	HasPlan    bool
}

type scheduleData struct {
	Weekday string
	Slot    Slot
}

type Server struct {
	templates *template.Template
	accounts  *AccountManager
}

func (s *Server) storeFor(r *http.Request) *Store {
	return s.accounts.storeFor(accountIDFromRequest(r))
}

func NewServer() http.Handler {
	return newServer("")
}

func NewPersistentServer(dataDir string) http.Handler {
	return newServer(dataDir)
}

func newServer(dataDir string) http.Handler {
	functions := template.FuncMap{
		"lessonData": func(date string, lesson Lesson, subjects, classes, students []string) lessonData {
			return lessonData{Date: date, Lesson: lesson, Subjects: subjects, Classes: classes, Students: students}
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

	server := &Server{templates: templates, accounts: newAccountManager(dataDir)}

	protected := http.NewServeMux()
	protected.HandleFunc("GET /", server.agenda)
	protected.HandleFunc("GET /year", server.year)
	protected.HandleFunc("GET /schedule", server.schedule)
	protected.HandleFunc("GET /notes", server.notes)
	protected.HandleFunc("GET /review", server.review)
	protected.HandleFunc("POST /agenda/{date}/lessons/{slot}", server.saveLesson)
	protected.HandleFunc("POST /agenda/{date}/lessons/{slot}/notes", server.addLessonNote)
	protected.HandleFunc("POST /agenda/{date}/override", server.saveDayOverride)
	protected.HandleFunc("POST /agenda/{date}/override/{index}/edit", server.updateDayOverride)
	protected.HandleFunc("POST /agenda/{date}/override/{index}/clear", server.clearDayOverride)
	protected.HandleFunc("POST /schedule", server.saveSchedule)
	protected.HandleFunc("POST /settings", server.saveSettings)
	protected.HandleFunc("POST /reset", server.resetData)
	protected.HandleFunc("GET /backup", server.downloadBackup)
	protected.HandleFunc("POST /restore", server.restoreBackup)
	protected.HandleFunc("POST /logout", server.logout)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /login", server.loginPage)
	mux.HandleFunc("POST /login", server.loginSubmit)
	mux.HandleFunc("GET /signup", server.signupPage)
	mux.HandleFunc("POST /signup", server.signupSubmit)
	mux.Handle("/", server.accounts.requireAuth(protected))
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
		Students:     []string{},
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

// lessonHasPlan reports whether a lesson has any recorded phase content or
// notes, so the review page can skip rendering an empty plan breakdown.
func lessonHasPlan(lesson Lesson) bool {
	if len(lesson.Notes) > 0 {
		return true
	}
	for _, phase := range lesson.Phases {
		if phase.Duration != "" || phase.Content != "" || phase.Materials != "" || phase.Notes != "" {
			return true
		}
	}
	return false
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
// When student is non-empty, only notes tagged with that student are included.
func (s *Store) allNotes(student string) []noteView {
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
				if student != "" && !strings.EqualFold(note.Student, student) {
					continue
				}
				notes = append(notes, noteView{
					Date: dateKey, DateLabel: date.Format("Monday, January 2, 2006"),
					Time: note.Time, Text: note.Text,
					Subject: lesson.Slot.Subject, Class: lesson.Slot.Class, Topic: lesson.Slot.Topic,
					Student: note.Student, SlotNumber: lesson.Slot.Number,
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

// noteStudents lists every student that can be filtered on: the configured
// roster plus any names typed directly into a note that aren't in it,
// deduplicated case-insensitively and sorted alphabetically.
func (s *Store) noteStudents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	var students []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		students = append(students, name)
	}
	for _, name := range s.data.Students {
		add(name)
	}
	for _, lessons := range s.data.Agendas {
		for _, lesson := range lessons {
			for _, note := range lesson.Notes {
				add(note.Student)
			}
		}
	}
	sort.Slice(students, func(i, j int) bool {
		return strings.ToLower(students[i]) < strings.ToLower(students[j])
	})
	return students
}

// lessonsFor collects every lesson matching subject and/or class across the
// whole school year, oldest first, so it reads like a coverage log. It merges
// the weekly timetable template with any per-day edits, same as the agenda
// page, so lessons that were never individually opened still show up. When
// readyOnly is true, only lessons marked ready to teach are included.
func (s *Store) lessonsFor(subject, class string, readyOnly bool) []reviewEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var entries []reviewEntry
	for date := schoolYearStart; !date.After(schoolYearEnd); date = date.AddDate(0, 0, 1) {
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		for _, lesson := range s.agendaLocked(date) {
			if lesson.Slot.Subject == "" && lesson.Slot.Class == "" {
				continue
			}
			if subject != "" && !strings.EqualFold(lesson.Slot.Subject, subject) {
				continue
			}
			if class != "" && !strings.EqualFold(lesson.Slot.Class, class) {
				continue
			}
			if readyOnly && !lesson.Complete {
				continue
			}
			entries = append(entries, reviewEntry{
				Date: date.Format(dateLayout), DateLabel: date.Format("Monday, January 2, 2006"),
				Time: lesson.Slot.Time, Subject: lesson.Slot.Subject, Class: lesson.Slot.Class,
				Topic: lesson.Slot.Topic, Complete: lesson.Complete, SlotNumber: lesson.Slot.Number,
				Lesson: lesson, HasPlan: lessonHasPlan(lesson),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date < entries[j].Date
		}
		return entries[i].SlotNumber < entries[j].SlotNumber
	})
	return entries
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

// reset wipes every lesson, note, event, and setting, restoring the app to
// its default starting state.
func (s *Store) reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = defaultStoreData()
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
	store := s.storeFor(r)
	s.render(w, "layout", s.agendaData(store, date, strings.TrimSpace(r.URL.Query().Get("subject"))))
}

func (s *Server) year(w http.ResponseWriter, r *http.Request) {
	store := s.storeFor(r)
	data := s.baseData(store, "year")
	store.mu.RLock()
	eventCounts := make(map[string]int, len(store.data.DayOverrides))
	for key, overrides := range store.data.DayOverrides {
		eventCounts[key] = len(overrides)
	}
	store.mu.RUnlock()
	data.Weeks = schoolWeeks(eventCounts)
	s.render(w, "layout", data)
}

func (s *Server) notes(w http.ResponseWriter, r *http.Request) {
	store := s.storeFor(r)
	data := s.baseData(store, "notes")
	data.NoteStudent = strings.TrimSpace(r.URL.Query().Get("student"))
	data.NoteStudents = store.noteStudents()
	data.Notes = store.allNotes(data.NoteStudent)
	s.render(w, "layout", data)
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	store := s.storeFor(r)
	data := s.baseData(store, "review")
	data.ReviewSubject = strings.TrimSpace(r.URL.Query().Get("subject"))
	data.ReviewClass = strings.TrimSpace(r.URL.Query().Get("class"))
	if r.URL.Query().Has("ready") {
		data.ReviewReadyOnly = r.URL.Query().Get("ready") != "0"
	} else {
		data.ReviewReadyOnly = true
	}
	if data.ReviewSubject != "" || data.ReviewClass != "" {
		data.ReviewLessons = store.lessonsFor(data.ReviewSubject, data.ReviewClass, data.ReviewReadyOnly)
		data.ReviewCount = len(data.ReviewLessons)
	}
	s.render(w, "layout", data)
}

func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	store := s.storeFor(r)
	data := s.baseData(store, "schedule")
	store.mu.RLock()
	data.Schedule = cloneSchedule(store.data.Schedule)
	store.mu.RUnlock()
	s.render(w, "layout", data)
}

func (s *Server) agendaData(store *Store, date time.Time, subject string) pageData {
	data := s.baseData(store, "agenda")
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
		data.SubjectWeek, data.SubjectCount = s.subjectWeek(store, date, subject)
	}
	if overrides := store.dayOverrides(date); len(overrides) > 0 {
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
	data.Lessons = store.agenda(date)
	data.TotalCount = len(data.Lessons)
	for _, lesson := range data.Lessons {
		if lesson.Complete {
			data.DoneCount++
		}
	}
	return data
}

func (s *Server) subjectWeek(store *Store, selected time.Time, subject string) ([]subjectDay, int) {
	start := selected.AddDate(0, 0, -weekdayOffset(selected.Weekday()))
	days := make([]subjectDay, 5)
	total := 0
	for index := range days {
		date := start.AddDate(0, 0, index)
		var matches []Lesson
		for _, lesson := range store.agenda(date) {
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

func (s *Server) baseData(store *Store, view string) pageData {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return pageData{
		View: view, Teacher: store.data.Teacher, School: store.data.School,
		Subjects: append([]string(nil), store.data.Subjects...), Classes: append([]string(nil), store.data.Classes...),
		Students: append([]string(nil), store.data.Students...),
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

	store := s.storeFor(r)
	lessons := store.agenda(date)
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
	if err := store.saveLessonOverride(date, slotIndex, lesson); err != nil {
		http.Error(w, "Could not save lesson", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "lessonSaved")
	store.mu.RLock()
	data := lessonData{
		Date: date.Format(dateLayout), Lesson: lesson,
		Subjects: append([]string(nil), store.data.Subjects...),
		Classes:  append([]string(nil), store.data.Classes...),
		Students: append([]string(nil), store.data.Students...),
	}
	store.mu.RUnlock()
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
	student := strings.TrimSpace(r.FormValue("student"))

	store := s.storeFor(r)
	lessons := store.agenda(date)
	lesson := lessons[slotIndex]
	lesson.Notes = append(lesson.Notes, LessonNote{Time: time.Now().Format("15:04"), Text: text, Student: student})
	if err := store.saveLessonOverride(date, slotIndex, lesson); err != nil {
		http.Error(w, "Could not save note", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "lessonSaved")
	store.mu.RLock()
	data := lessonData{
		Date: date.Format(dateLayout), Lesson: lesson,
		Subjects: append([]string(nil), store.data.Subjects...),
		Classes:  append([]string(nil), store.data.Classes...),
		Students: append([]string(nil), store.data.Students...),
	}
	store.mu.RUnlock()
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
	store := s.storeFor(r)
	override := DayOverride{
		Title:      title,
		Notes:      strings.TrimSpace(r.FormValue("notes")),
		Activities: parseActivities(r),
	}
	if err := store.addDayOverride(schoolDatesInRange(start, end), override); err != nil {
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
	store := s.storeFor(r)
	override := DayOverride{
		Title:      title,
		Notes:      strings.TrimSpace(r.FormValue("notes")),
		Activities: parseActivities(r),
	}
	if err := store.updateDayOverride(date, index, override); err != nil {
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
	if err := s.storeFor(r).removeDayOverride(date, index); err != nil {
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

	store := s.storeFor(r)
	store.mu.Lock()
	for _, weekday := range weekdays() {
		slots := store.data.Schedule[weekday]
		for index := range slots {
			prefix := fmt.Sprintf("%s-%d-", weekday, slots[index].Number)
			slots[index].Time = strings.TrimSpace(r.FormValue(prefix + "time"))
			slots[index].Subject = strings.TrimSpace(r.FormValue(prefix + "subject"))
			slots[index].Class = strings.TrimSpace(r.FormValue(prefix + "class"))
			slots[index].Topic = strings.TrimSpace(r.FormValue(prefix + "topic"))
		}
	}
	err := store.persistLocked()
	store.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not save timetable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "lessonSaved")
	data := s.baseData(store, "schedule")
	store.mu.RLock()
	data.Schedule = cloneSchedule(store.data.Schedule)
	store.mu.RUnlock()
	s.render(w, "schedule-tabs", data)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	store := s.storeFor(r)
	store.mu.Lock()
	store.data.Teacher = strings.TrimSpace(r.FormValue("teacher"))
	store.data.School = strings.TrimSpace(r.FormValue("school"))
	store.data.Subjects = splitLines(r.FormValue("subjects"))
	store.data.Classes = splitLines(r.FormValue("classes"))
	store.data.Students = splitLines(r.FormValue("students"))
	err := store.persistLocked()
	store.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not save settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/schedule")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetData(w http.ResponseWriter, r *http.Request) {
	if err := s.storeFor(r).reset(); err != nil {
		http.Error(w, "Could not reset data", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	store := s.storeFor(r)
	store.mu.RLock()
	defer store.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=lehrerin-backup-%s.json", accountIDFromRequest(r)))
	if err := json.NewEncoder(w).Encode(store.data); err != nil {
		http.Error(w, "Could not create backup", http.StatusInternalServerError)
	}
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Could not read backup file", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		http.Error(w, "Please choose a backup file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	var restored storeData
	decoder := json.NewDecoder(io.LimitReader(file, 10<<20))
	if err := decoder.Decode(&restored); err != nil || restored.Schedule == nil || restored.Agendas == nil || restored.DayOverrides == nil {
		http.Error(w, "That backup file is invalid", http.StatusBadRequest)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		http.Error(w, "That backup file is invalid", http.StatusBadRequest)
		return
	}

	store := s.storeFor(r)
	store.mu.Lock()
	store.data = restored
	err = store.persistLocked()
	store.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not restore backup", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusNoContent)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("backup contains trailing data")
	}
	return nil
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
	var weeks []weekRow
	for monday := schoolYearStart; !monday.After(schoolYearEnd); monday = monday.AddDate(0, 0, 7) {
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
