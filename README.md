# Lehrerin

A teacher agenda built with Go templates and HTMX. It provides a full school-year calendar, reusable weekday timetables, and detailed daily lesson plans for content, materials, differentiation, and assessment.

Changes are stored locally in `data/lehrerin.json`. Daily agendas inherit the matching weekday timetable until that date is edited.

## Run locally

```sh
go run ./cmd/lehrerin
```

Open http://localhost:8080. Set `PORT` to use a different port.

Use **Print / PDF** on an agenda to print it or save it as a PDF from the browser.

## Test

```sh
go test ./...
```