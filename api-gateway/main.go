package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	processorURL := os.Getenv("PROCESSOR_URL")
	if processorURL == "" {
		processorURL = "http://processor/processing"
	}

	client := &http.Client{Timeout: 5 * time.Second}

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.Post(processorURL, "application/json", nil)
		if err != nil {
			log.Printf("processor error: %v", err)
			http.Error(w, "processor unavailable", http.StatusBadGateway)
			return
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			http.Error(w, "processor returned non-200", http.StatusBadGateway)
			return
		}

		fmt.Fprintln(w, "hello world")
	})

	log.Println("api-gateway listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
