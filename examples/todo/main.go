// A tiny in-memory TODO API showing the main Kite features.
//
//	go run ./examples/todo
//	curl -X POST localhost:8080/api/v1/todos -H 'Content-Type: application/json' -d '{"title":"ลองใช้ Kite"}'
//	curl localhost:8080/api/v1/todos/1
package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/tfsoft-tech/kite"
)

type Todo struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type store struct {
	mu    sync.RWMutex
	next  int
	items map[int]Todo
}

func main() {
	s := &store{items: map[int]Todo{}}

	app := kite.New(kite.Config{RedirectTrailingSlash: true})
	app.Use(kite.Recover(), kite.RequestID(), kite.Logger())

	app.GET("/health", func(c *kite.Ctx) error { return c.String("ok") })

	api := app.Group("/api/v1", kite.Timeout(5*time.Second))

	api.GET("/todos", func(c *kite.Ctx) error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]Todo, 0, len(s.items))
		for _, t := range s.items {
			out = append(out, t)
		}
		return c.JSON(out)
	})

	api.GET("/todos/:id", func(c *kite.Ctx) error {
		id, err := c.ParamInt("id")
		if err != nil {
			return err
		}
		s.mu.RLock()
		t, ok := s.items[id]
		s.mu.RUnlock()
		if !ok {
			return kite.NewError(http.StatusNotFound, "todo not found")
		}
		return c.JSON(t)
	})

	api.POST("/todos", func(c *kite.Ctx) error {
		var in struct {
			Title string `json:"title"`
		}
		if err := c.Bind(&in); err != nil {
			return err
		}
		if in.Title == "" {
			return kite.NewError(http.StatusUnprocessableEntity, "title is required")
		}
		s.mu.Lock()
		s.next++
		t := Todo{ID: s.next, Title: in.Title}
		s.items[t.ID] = t
		s.mu.Unlock()
		return c.Status(http.StatusCreated).JSON(t)
	})

	api.DELETE("/todos/:id", func(c *kite.Ctx) error {
		id, err := c.ParamInt("id")
		if err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.items, id)
		s.mu.Unlock()
		return c.NoContent(http.StatusNoContent)
	})

	if err := app.Run(":8080"); err != nil {
		panic(err)
	}
}
