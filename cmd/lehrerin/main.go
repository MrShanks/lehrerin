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

	handler := web.NewPersistentServer("data")

	log.Printf("Lehrerin is ready at http://localhost%s", address)
	if err := http.ListenAndServe(address, handler); err != nil {
		log.Fatal(err)
	}
}
