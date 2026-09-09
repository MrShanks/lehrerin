# Lehrerin

A teacher agenda built with Go templates and HTMX. It provides a full school-year calendar, reusable weekday timetables, and detailed daily lesson plans for content, materials, differentiation, and assessment.

Changes are stored locally in `data/lehrerin.json`. Daily agendas inherit the matching weekday timetable until that date is edited.

## Run locally

```sh
go run ./cmd/lehrerin
```

Open http://localhost:8080. Set `PORT` to use a different port.

Use **Print / PDF** on an agenda to print it or save it as a PDF from the browser.

## Automatic email delivery

Lehrerin can email each teacher an HTML copy of their next school day or next school week. Delivery schedules and recipient addresses are configured per teacher in **Planner settings** and run in the `Europe/Zurich` timezone.

For Docker/NAS deployments, configure Gmail SMTP directly under the `lehrerin` service in `docker-compose.yml`:

```yaml
environment:
	PORT: "8080"
	SMTP_HOST: "smtp.gmail.com"
	SMTP_PORT: "587"
	SMTP_USERNAME: "your-account@gmail.com"
	SMTP_PASSWORD: "your-google-app-password"
	SMTP_FROM: "your-account@gmail.com"
```

Replace the placeholder values in the Compose file before deploying. Use a Google app password, not the account's normal password. The Gmail account must have 2-Step Verification enabled before an app password can be created.

The server checks schedules once per minute. Daily emails contain the next weekday's plan; weekly emails contain the next Monday through Friday. Successful deliveries are recorded in each teacher's data so restarting the app does not resend the same plan.

## Test

```sh
go test ./...
```