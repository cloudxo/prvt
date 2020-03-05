/*
Copyright © 2020 Alessandro Segala (@ItalyPaleAle)

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/

package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"e2e/fs"

	rice "github.com/GeertJohan/go.rice"
	"github.com/gin-gonic/gin"
)

type Server struct {
	Store fs.Fs
}

func (s *Server) Start() error {
	// Start gin server
	router := gin.Default()

	// Add routes
	router.GET("/file/:fileId", s.FileHandler)
	{
		// APIs
		apis := router.Group("/api")
		apis.GET("/tree/*path", s.TreeHandler)
	}

	// UI
	uiBox := rice.MustFindBox("ui")
	router.StaticFS("/ui", uiBox.HTTPBox())

	// Redirect from / to the UI
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/ui")
	})

	// HTTP Server
	server := &http.Server{
		Addr:           "127.0.0.1:3000",
		Handler:        router,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Handle graceful shutdown on SIGINT
	idleConnsClosed := make(chan struct{})
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, os.Interrupt, syscall.SIGTERM)
		<-s

		// We received an interrupt signal, shut down.
		if err := server.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			fmt.Printf("HTTP server shutdown error: %v\n", err)
		}
		close(idleConnsClosed)
	}()

	// Listen to connections
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	<-idleConnsClosed

	return nil
}
