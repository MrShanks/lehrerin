package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/MrShanks/lehrerin/internal/web"
)

func main() {
	loadDotEnv(".env")

	address := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		address = ":" + port
	}

	handler := web.NewPersistentServer("data")

	log.Printf("Lehrerin is ready at http://localhost%s", address)
	if err := http.ListenAndServe(address, handler); err != nil {
		log.Fatal(err)
	}
}

// loadDotEnv sets environment variables from a simple KEY=VALUE file, without
// overriding anything already set in the real environment. Docker Compose
// reads .env itself, so this only matters when running the binary directly.
func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue
		}
		os.Setenv(key, strings.TrimSpace(value))
	}
}
