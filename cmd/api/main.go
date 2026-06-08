package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// healthz returns "ok" if the server is running.
func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK) // set status code to 200
	_, _ = w.Write([]byte("ok"))
}

func main() {
	r := chi.NewRouter()
	r.Get("/healthz", healthz)

	http.ListenAndServe(":8080", r)
}
