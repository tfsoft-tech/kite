package main

import (
	"encoding/json"
	"net/http"
	"os"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	m := http.NewServeMux()
	m.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(user{r.PathValue("id"), "gopher"})
	})
	http.ListenAndServe(os.Args[1], m)
}
