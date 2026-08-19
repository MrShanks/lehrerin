package main

import (
	"log"
	"net/http"
	"os"

	"github.com/MrShanks/lehrerin/internal/web"
)

func main() {
	address := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		address = ":" + port
	}

	log.Printf("Lehrerin is ready at http://localhost%s", address)
	if err := http.ListenAndServe(address, web.NewPersistentServer("data/lehrerin.json")); err != nil {
		log.Fatal(err)
	}
}
