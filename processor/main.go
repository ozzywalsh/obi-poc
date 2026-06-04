package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/processing", func(w http.ResponseWriter, r *http.Request) {
		log.Println("processing request")
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(w, "ok")
	})

	log.Println("processor listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
