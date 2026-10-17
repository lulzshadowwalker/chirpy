package main

import (
	"fmt"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	fs := http.StripPrefix("/app", http.FileServer(http.Dir("app")))
	mux.Handle("/", fs)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})

	s := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println(s.ListenAndServe())
}
