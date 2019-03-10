package main

import (
	"flag"
	"fmt"
	"logster"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/rjeczalik/notify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/BurntSushi/toml"
)

const version = "0.1"

func main() {

	// Initialize logging using zerolog
	zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"
	if true {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: zerolog.TimeFieldFormat})
	}
	log.Info().Msg("Starting logster v" + version)

	configFile := flag.String("conf", "logsterc.conf", "name of the configuration file to load")
	flag.Parse()

	var conf logster.ClientConfiguration
	if _, err := toml.DecodeFile(*configFile, &conf); err != nil {
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}

	client := logster.NewClient(conf.Server)
	log.Info().Msgf("Logster client to server %s, creating streams", conf.Server)

	// Setup syncronization of goroutines
	var wg sync.WaitGroup

	// Make the channel buffered to ensure no event is dropped. Notify will drop
	// an event if the receiver is not able to keep up the sending pace.
	eventCh := make(chan notify.EventInfo, 5)
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)

	// Set up a watchpoint listening for inotify-specific events within a
	// current working directory. Dispatch each InCloseWrite and InMovedTo
	// events separately to c.
	/*
		if err := notify.Watch(path, eventCh, notify.InCloseWrite, notify.InMovedTo, notify.InModify); err != nil {
			log.Fatal().Err(err)
		}
		defer notify.Stop(eventCh)
		log.Info().Str("path", path).Msg("Starting watch")
	*/

	wg.Add(3)
	log.Info().Msg("Setup signal, watch and heartbeat handlers")
	go handleSignal(signalCh)
	go handleWatch(eventCh, client)
	go handleHeartbeatTimer()

	for idx, stream := range conf.FileStreams {
		log.Debug().Msgf("Process stream #%d: %s", idx, stream)

		name := stream.Name
		path := stream.Path
		log.Debug().Msgf("Init stream %s: %s", name, path)
		// Should add watch now
		s, err := client.NewLogStream(name, path)
		if err != nil {
			log.Error().Msgf("Failed to start stream %s:%s", name, path)
		}
		log.Info().Msgf("Starting stream %s:%s", name, path)
		wg.Add(1)
		go s.StreamFileHandler(path, 0)
		log.Info().Msgf("Stream %s initialized", name)
	}

	wg.Wait()
	quit(0)
}

func quit(code int) {
	log.Info().Msg("Done.")
	os.Exit(code)
}

func handleSignal(ch <-chan os.Signal) {
	for sig := range ch {
		switch sig {
		case os.Interrupt:
			log.Info().Str("signal", sig.String()).Msg("Ignoring interrupt signal")
			quit(0)
		default:
			log.Info().Str("signal", sig.String()).Msg("Ignoring unhandled signal")
		}
	}
}

func handleHeartbeatTimer() {
	timer := time.NewTicker(time.Second * 10)

	for t := range timer.C {
		log.Debug().Time("timer", t).Msg("Heartbeat timer")
	}
}

func handleWatch(ch <-chan notify.EventInfo, client *logster.Client) {
	for ei := range ch {
		// Block until an event is received.
		switch ei.Event() {
		case notify.InModify:
			log.Info().Str("path", ei.Path()).Msg("File was modified.")
			// client.SendToStreamByPath(ei.Path(), "FILEMOD "+ei.Path()+"\n")
		case notify.InCloseWrite:
			log.Info().Str("path", ei.Path()).Msg("File was closed.")
			// go client.SendToStreamByPath(ei.Path(), "data")
		case notify.InMovedTo:
			log.Info().Str("path", ei.Path()).Msg("File was moved.")
		}
	}
}
