package main

import (
	"github.com/labstack/echo/v4"
	"os"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/users/:id", func(c echo.Context) error { return c.JSON(200, user{c.Param("id"), "gopher"}) })
	e.Start(os.Args[1])
}
