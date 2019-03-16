package main

import (
	"flag"
	"fmt"
	"logster"
	"os"
	"sync"

	"github.com/BurntSushi/toml"
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

	configFile := flag.String("conf", "logsterd.conf", "name of the configuration file to load")
	flag.Parse()

	var conf logster.ServerConfiguration
	if _, err := toml.DecodeFile(*configFile, &conf); err != nil {
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}

	server, err := logster.NewServer(conf.Listen, conf.OutputDirectory, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to connect")
	}
	log.Info().Str("server", server.Address).Msg("Started server")

	var wg sync.WaitGroup
	wg.Add(1)
	if conf.Prometheus.Enabled {
		listen := conf.Prometheus.Listen
		if listen == "" {
			listen = "localhost:9099"
		}
		wg.Add(1)
		go logster.ListenPrometheus(listen)
	}

	wg.Wait()
	quit(0)
}

func quit(code int) {
	log.Info().Msg("Done.")
	os.Exit(code)
}
