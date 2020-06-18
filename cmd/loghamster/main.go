package main

import (
	"flag"
	"log/syslog"
	"loghamster"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jinzhu/configor"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var version = "1.0.7"
var config loghamster.Configuration

func main() {

	// Initialize logging using zerolog
	zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000000" // alternative zerolog.TimeFormatUnixMs
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	consoleLog := log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: zerolog.TimeFieldFormat})
	log.Logger = consoleLog

	log.Info().Int("pid", os.Getpid()).Msgf("Starting Hydra-HTTP v%s", version)

	conf := &config

	// Define and parse commandline flags for initial configuration
	configFile := flag.String("config", "loghamster.conf", "Configuration file to load")

	flag.BoolVar(&conf.Debug, "debug", false, "Enable debug")
	flag.BoolVar(&conf.Syslog.Enabled, "syslog", false, "Enable logging to syslog")
	flag.Parse()

	if conf.Debug {
		log.Debug().Msg("Enabling debug logging")
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Debug().Str("config", *configFile).Msg("Loading configuration")
	err := configor.New(&configor.Config{Debug: conf.Debug}).Load(conf, *configFile)
	if err != nil {
		log.Fatal().Str("config", *configFile).Err(err).Msg("Unable to load configuration")
	}
	// Reparse commandline flags to override loaded config parameters
	flag.Parse()

	// Adjust logging based on configuration
	if conf.Syslog.Enabled {
		network := ""
		if conf.Syslog.Hostname != "" {
			network = "udp"
		}
		syslogWriter, err := syslog.Dial(network, conf.Syslog.Hostname, syslog.LOG_ERR, conf.Syslog.ApplicationName)
		defer syslogWriter.Close()
		if err != nil {
			log.Fatal().Str("host", conf.Syslog.Hostname).Err(err).Msg("Failed to connect to syslog")
		}
		log.Info().Str("host", conf.Syslog.Hostname).Str("name", conf.Syslog.ApplicationName).Msg("Changing logging to syslog")
		log.Logger = log.Output(zerolog.SyslogLevelWriter(syslogWriter))
	}

	// Change global loglevel if requested
	if conf.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Start CPU/memory profiling webserver if enabled
	if conf.Profile.Enabled {
		log.Info().Str("port", conf.Profile.Port).Msg("Starting CPU/memory profiling")
		go http.ListenAndServe(conf.Profile.Port, http.DefaultServeMux)
	}

	// Initialize input/output files
	files := loghamster.NewFileManager()
	// Process all file inputs
	for _, f := range conf.Input {
		log.Info().Str("path", f.Path).Msg("Add configured input file")
		files.AddInput(loghamster.InputFile{
			Name:  f.Name,
			Path:  f.Path,
			Watch: f.Watch,
		})
	}
	// Process all file outputs
	for _, f := range conf.Output {
		log.Info().Str("path", f.Path).Msg("Add configured output file")
		files.AddOutput(loghamster.OutputFile{
			Name:     f.Name,
			Path:     f.Path,
			Compress: f.Compress,
		})
	}

	// Setup syncronization of goroutines
	var wg sync.WaitGroup
	// Make the channel buffered to ensure no event is dropped. Notify will drop
	// an event if the receiver is not able to keep up the sending pace.
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to initialize filesystem watcher")
	}
	defer watcher.Close()

	wg.Add(2)
	log.Info().Msg("Setup signal, watch and heartbeat handlers")
	go handleSignal(signalCh)
	go handleHeartbeatTimer()

	// Detect mode if not defined
	if conf.Mode == "" {
		if conf.Target.Hostname == "" {
			conf.Mode = "server"
		} else {
			conf.Mode = "client"
		}
	}

	if conf.Mode == "server" {
		server, err := loghamster.NewServer(conf.Server.ListenAddress, conf.Server.BaseDirectory, files)
		if err != nil {
			log.Error().Err(err).Msg("Failed to connect")
		}
		log.Info().Str("server", server.Address).Msg("Started server")

		wg.Add(1)

	} else {

		client := loghamster.NewClient(conf.Target.Hostname, files)
		log.Info().Msgf("LogHamster client to server %s, creating streams", conf.Server)

		wg.Add(1)
		go handleWatch(watcher, client)

		for idx, file := range files.Inputs {
			log.Debug().Msgf("Process stream #%d: %s", idx, file)

			name := file.Name
			path := file.Path
			dir := filepath.Dir(path)

			// Set up a watch listening for filesystem notifications within the
			// directory of the provided file
			log.Info().Str("dir", dir).Str("file", path).Msg("Watch directory of file for changes")
			err = watcher.Add(dir)
			if err != nil {
				log.Error().Err(err).Str("file", path).Str("dir", dir).Msg("Failed to watch dir for file")
			}
			err = watcher.Add(dir)
			if err != nil {
				log.Error().Err(err).Str("file", path).Str("dir", dir).Msg("Failed to watch dir for file")
			}

			log.Debug().Str("name", name).Str("path", path).Msg("Init stream")
			// Should add watch now
			s, err := client.NewLogStream(name, path)
			if err != nil {
				log.Error().Err(err).Str("name", name).Str("path", path).Msg("Failed to start stream")
			}
			log.Info().Str("name", name).Str("path", path).Msg("Starting stream")
			wg.Add(1)
			go s.StreamFile(path, 0)
			log.Debug().Str("name", name).Msg("Stream init succeeded")
		}
	}

	if conf.Prometheus.Enabled {
		listen := conf.Prometheus.Listen
		if listen == "" {
			listen = "localhost:9099"
		}
		wg.Add(1)
		go loghamster.ListenPrometheus(listen)
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

func handleWatch(watcher *fsnotify.Watcher, client *loghamster.Client) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				log.Debug().Msg("not ok for event watcher")
				return
			}
			log.Debug().Str("path", event.Name).Str("event", event.Op.String()).Msg("received event")
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Debug().Str("file", event.Name).Msg("File changed, trigger file change handler")
				// On a detected write for a watched file the stream should
				if err := client.HandleFileChange(event.Name); err != nil {
					log.Error().Err(err).Str("path", event.Name).Msg("Error for handling file creation")
				}
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				log.Debug().Str("file", event.Name).Msg("File created, trigger file create handler")
				if err := client.HandleFileCreate(event.Name); err != nil {
					log.Error().Err(err).Str("path", event.Name).Msg("Error for handling file creation")
				}
			}
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				log.Debug().Str("file", event.Name).Msg("File removed, trigger file delete handler")
				if err := client.HandleFileDelete(event.Name); err != nil {
					log.Error().Err(err).Str("path", event.Name).Msg("Error for handling file creation")
				}
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				log.Debug().Str("file", event.Name).Msg("File renamed, trigger file delete handler")
				if err := client.HandleFileDelete(event.Name); err != nil {
					log.Error().Err(err).Str("path", event.Name).Msg("Error for handling file creation")
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				log.Debug().Msg("not ok for error watcher")
				return
			}
			log.Debug().Err(err).Msg("error watching file")
		}
	}
}
