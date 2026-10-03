package main

import (
	"github.com/tfsoft-tech/kite"
	"os"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	a := kite.New()
	a.GET("/users/:id", func(c *kite.Ctx) error { return c.JSON(user{c.Param("id"), "gopher"}) })
	a.Server(os.Args[1]).ListenAndServe()
}
