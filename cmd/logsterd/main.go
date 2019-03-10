package main

import (
	"logster"
	"os"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {

	// Initialize logging using zerolog
	zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"
	if true {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: zerolog.TimeFieldFormat})
	}
	log.Info().Msg("Starting up")

	server, err := logster.NewServer("127.0.0.1:8000")
	if err != nil {
		log.Error().Err(err).Msg("Failed to connect")
	}
	log.Info().Str("server", server.Address).Msg("Started server")

	/*
		// Echo instance
		e := echo.New()

		// Middleware
		e.Use(middleware.Logger())
		e.Use(middleware.Recover())

		// Routes
		e.GET("/", hello)

		// Start server
		e.Logger.Fatal(e.Start(":8901"))
	*/

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
	quit(0)
}

/*
// Handler
func hello(c echo.Context) error {
	return c.String(http.StatusOK, "Hello, World!")
}
*/

func quit(code int) {
	log.Info().Msg("Done.")
	os.Exit(code)
}
