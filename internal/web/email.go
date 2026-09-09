package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

const deliveryTimezone = "Europe/Zurich"

type emailSender interface {
	Send(to, subject, filename string, attachment []byte) error
}

type smtpEmailSender struct {
	host, address, username, password, from string
}

func newSMTPEmailSenderFromEnv() emailSender {
	username := strings.TrimSpace(os.Getenv("SMTP_USERNAME"))
	password := os.Getenv("SMTP_PASSWORD")
	if username == "" || password == "" {
		return nil
	}
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		host = "smtp.gmail.com"
	}
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	if from == "" {
		from = username
	}
	return &smtpEmailSender{host: host, address: host + ":" + port, username: username, password: password, from: from}
}

func (s *smtpEmailSender) Send(to, subject, filename string, attachment []byte) error {
	boundary := "lehrerin-lesson-plan"
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", s.from, to, subject, boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYour automatic Lehrerin lesson plan is attached.\r\n", boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/html; charset=utf-8; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, filename, filename)
	encoded := base64.StdEncoding.EncodeToString(attachment)
	for len(encoded) > 76 {
		message.WriteString(encoded[:76])
		message.WriteString("\r\n")
		encoded = encoded[76:]
	}
	message.WriteString(encoded)
	message.WriteString("\r\n--")
	message.WriteString(boundary)
	message.WriteString("--\r\n")
	return smtp.SendMail(s.address, smtp.PlainAuth("", s.username, s.password, s.host), s.from, []string{to}, message.Bytes())
}

func deliveryTime(value string) string {
	if parsed, err := time.Parse("15:04", value); err == nil {
		return parsed.Format("15:04")
	}
	return "18:00"
}

func deliveryWeekday(value string) string {
	for _, weekday := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if value == weekday {
			return value
		}
	}
	return "Sunday"
}

func nextSchoolDay(now time.Time) time.Time {
	date := now.AddDate(0, 0, 1)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

func nextSchoolWeek(now time.Time) time.Time {
	days := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if days == 0 {
		days = 7
	}
	return now.AddDate(0, 0, days)
}

func deliveryDue(now time.Time, scheduledTime string) bool {
	return now.Format("15:04") == scheduledTime
}

func (s *Server) startEmailScheduler() {
	if s.emailSender == nil {
		return
	}
	go func() {
		s.sendScheduledEmails(time.Now())
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			s.sendScheduledEmails(now)
		}
	}()
}

func (s *Server) sendScheduledEmails(now time.Time) {
	location, err := time.LoadLocation(deliveryTimezone)
	if err != nil {
		log.Printf("email scheduler: load timezone: %v", err)
		return
	}
	now = now.In(location)
	for _, account := range s.accounts.listAccounts() {
		store := s.accounts.storeFor(account.ID)
		if err := s.sendScheduledForStore(store, now); err != nil {
			log.Printf("email scheduler: account %s: %v", account.ID, err)
		}
	}
}

func (s *Server) sendScheduledForStore(store *Store, now time.Time) error {
	store.mu.RLock()
	settings := store.data
	store.mu.RUnlock()
	if settings.Email == "" {
		return nil
	}
	if settings.DailyEmail && deliveryDue(now, deliveryTime(settings.DailyTime)) {
		target := nextSchoolDay(now)
		key := target.Format(dateLayout)
		if settings.LastDaily != key {
			attachment, filename, subject, err := renderDayAttachment(store, target)
			if err != nil {
				return err
			}
			if err := s.emailSender.Send(settings.Email, subject, filename, attachment); err != nil {
				return err
			}
			store.mu.Lock()
			store.data.LastDaily = key
			err = store.persistLocked()
			store.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
	if settings.WeeklyEmail && now.Weekday().String() == deliveryWeekday(settings.WeeklyDay) && deliveryDue(now, deliveryTime(settings.WeeklyTime)) {
		start := nextSchoolWeek(now)
		key := start.Format(dateLayout)
		if settings.LastWeekly != key {
			attachment, filename, subject, err := renderWeekAttachment(store, start)
			if err != nil {
				return err
			}
			if err := s.emailSender.Send(settings.Email, subject, filename, attachment); err != nil {
				return err
			}
			store.mu.Lock()
			store.data.LastWeekly = key
			err = store.persistLocked()
			store.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) sendTestEmail(store *Store, to string, now time.Time) error {
	target := nextSchoolDay(now)
	attachment, filename, _, err := renderDayAttachment(store, target)
	if err != nil {
		return err
	}
	return s.emailSender.Send(to, "Lehrerin test: plan for "+target.Format("Monday, January 2, 2006"), filename, attachment)
}

func (s *Server) testEmail(w http.ResponseWriter, r *http.Request) {
	if s.emailSender == nil {
		w.Write([]byte("Gmail is not configured on the server."))
		return
	}
	if err := r.ParseForm(); err != nil {
		w.Write([]byte("Could not read the email address."))
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if !validDeliveryEmail(email) {
		w.Write([]byte("Enter a valid email address first."))
		return
	}
	if err := s.sendTestEmail(s.storeFor(r), email, time.Now()); err != nil {
		log.Printf("test email for account %s: %v", accountIDFromRequest(r), err)
		w.Write([]byte("Test email could not be sent. Check the server mail configuration."))
		return
	}
	w.Write([]byte("Tomorrow's school-day plan was sent. Save settings and enable a schedule for automatic delivery."))
}

func validDeliveryEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}
