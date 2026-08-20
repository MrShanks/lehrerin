package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

type downloadDayData struct {
	Teacher   string
	School    string
	Date      string
	Lessons   []Lesson
	Overrides []DayOverride
}

type downloadWeekDay struct {
	Date      string
	Lessons   []Lesson
	Overrides []DayOverride
}

type downloadWeekData struct {
	Teacher string
	School  string
	Start   string
	End     string
	Days    []downloadWeekDay
}

var downloadTemplate = template.Must(template.New("download").Funcs(template.FuncMap{"or": func(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}}).Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title>
<style>
body{margin:0;padding:32px;color:#202622;background:#f5f3ed;font:15px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:900px;margin:auto;padding:32px;background:#fffefb}h1{margin:0 0 4px;color:#235c48}h2{margin:28px 0 12px;border-bottom:2px solid #d86850;padding-bottom:6px}h3{margin:0;color:#235c48}p{margin:5px 0}.meta{color:#68716a}.lesson{margin:16px 0;padding:16px;border:1px solid #d9ddd7;border-left:4px solid #d86850;border-radius:5px}.lesson.complete{border-left-color:#235c48}.lesson-head{display:flex;justify-content:space-between;gap:16px}.lesson-head strong{font-size:17px}.phase{margin-top:12px;padding-top:10px;border-top:1px solid #d9ddd7}.phase strong{color:#235c48}.phase p{white-space:pre-wrap}.notes{margin-top:10px;padding:10px;background:#f5f3ed}.event{margin:12px 0;padding:14px;border:1px solid #d9ddd7;border-left:4px solid #e5b74f}.activity{padding:5px 0;border-top:1px solid #d9ddd7}.activity:first-child{border-top:0}.week-day{break-inside:avoid}@media print{body{padding:0;background:white}main{padding:0;max-width:none}.lesson{break-inside:avoid}}
</style></head><body><main>{{if eq .Kind "day"}}{{template "day" .}}{{else}}{{template "week" .}}{{end}}</main></body></html>
{{define "day"}}<h1>Daily lesson plan</h1><p class="meta">{{.Data.Date}} · {{.Data.Teacher}}{{if .Data.School}} · {{.Data.School}}{{end}}</p>{{range .Data.Overrides}}{{template "event" .}}{{else}}{{range .Data.Lessons}}{{template "lesson" .}}{{else}}<p>No lessons scheduled.</p>{{end}}{{end}}{{end}}
{{define "week"}}<h1>Weekly lesson plan</h1><p class="meta">{{.Data.Start}} – {{.Data.End}} · {{.Data.Teacher}}{{if .Data.School}} · {{.Data.School}}{{end}}</p>{{range .Data.Days}}<section class="week-day"><h2>{{.Date}}</h2>{{range .Overrides}}{{template "event" .}}{{else}}{{range .Lessons}}{{template "lesson" .}}{{else}}<p>No lessons scheduled.</p>{{end}}{{end}}</section>{{end}}{{end}}
{{define "lesson"}}<article class="lesson{{if .Complete}} complete{{end}}"><div class="lesson-head"><strong>{{.Slot.Time}} · {{.Slot.Subject}}{{if .Slot.Class}} · {{.Slot.Class}}{{end}}</strong>{{if .Complete}}<span>Ready</span>{{end}}</div>{{if .Slot.Topic}}<p><strong>Topic:</strong> {{.Slot.Topic}}</p>{{end}}{{range .Phases}}{{if or .Content .Materials .Notes}}<section class="phase"><strong>{{.Name}}{{if .Duration}} · {{.Duration}} min{{end}}</strong>{{if .Content}}<p><b>Plan:</b> {{.Content}}</p>{{end}}{{if .Materials}}<p><b>Materials:</b> {{.Materials}}</p>{{end}}{{if .Notes}}<p><b>Notes:</b> {{.Notes}}</p>{{end}}</section>{{end}}{{end}}{{range .Notes}}<div class="notes"><b>Lesson note{{if .Student}} for {{.Student}}{{end}}:</b> {{.Text}}</div>{{end}}</article>{{end}}
{{define "event"}}<article class="event"><h3>{{.Title}}</h3>{{if .Notes}}<p>{{.Notes}}</p>{{end}}{{range .Activities}}<div class="activity"><b>{{.Time}} · {{.Name}}</b>{{if .Material}} · {{.Material}}{{end}}{{if .Description}}<p>{{.Description}}</p>{{end}}{{if .Notes}}<p><b>Notes:</b> {{.Notes}}</p>{{end}}</div>{{end}}</article>{{end}}
`))

func (s *Server) downloadDay(w http.ResponseWriter, r *http.Request) {
	date := downloadDate(r.URL.Query().Get("date"))
	store := s.storeFor(r)
	data := downloadDayDataFor(store, date)
	sendDownload(w, "day", date.Format(dateLayout), data)
}

func (s *Server) downloadWeek(w http.ResponseWriter, r *http.Request) {
	date := downloadDate(r.URL.Query().Get("date"))
	start := date.AddDate(0, 0, -weekdayOffset(date.Weekday()))
	store := s.storeFor(r)
	days := make([]downloadWeekDay, 5)
	for index := range days {
		day := start.AddDate(0, 0, index)
		data := downloadDayDataFor(store, day)
		days[index] = downloadWeekDay{Date: data.Date, Lessons: data.Lessons, Overrides: data.Overrides}
	}
	sendDownload(w, "week", start.Format(dateLayout), downloadWeekData{Teacher: dataTeacher(store), School: dataSchool(store), Start: start.Format("Monday, January 2, 2006"), End: start.AddDate(0, 0, 4).Format("Monday, January 2, 2006"), Days: days})
}

func downloadDate(raw string) time.Time {
	if parsed, err := time.Parse(dateLayout, raw); err == nil {
		return parsed
	}
	return time.Now()
}

func downloadDayDataFor(store *Store, date time.Time) downloadDayData {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return downloadDayData{Teacher: store.data.Teacher, School: store.data.School, Date: date.Format("Monday, January 2, 2006"), Lessons: store.agendaLocked(date), Overrides: append([]DayOverride(nil), store.data.DayOverrides[date.Format(dateLayout)]...)}
}

func dataTeacher(store *Store) string {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.data.Teacher
}
func dataSchool(store *Store) string {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.data.School
}

func sendDownload(w http.ResponseWriter, kind, date string, data any) {
	filename := fmt.Sprintf("lehrerin-%s-%s.html", kind, date)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_ = downloadTemplate.Execute(w, struct {
		Kind  string
		Title string
		Data  any
	}{kind, "Lehrerin lesson plan", data})
}
