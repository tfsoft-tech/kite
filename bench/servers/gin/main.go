package main

import (
	"github.com/gin-gonic/gin"
	"os"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.GET("/users/:id", func(c *gin.Context) { c.JSON(200, user{c.Param("id"), "gopher"}) })
	e.Run(os.Args[1])
}
