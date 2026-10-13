package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	// Register the endpoint and its response.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Configure the HTTP server.
	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("Starting API on port 8080")
	log.Fatal(server.ListenAndServe())
}