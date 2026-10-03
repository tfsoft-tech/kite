package main

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"os"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	r := chi.NewRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(user{chi.URLParam(r, "id"), "gopher"})
	})
	http.ListenAndServe(os.Args[1], r)
}
