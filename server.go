package loghamster

import (
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

// Server handles a loghamster client connection
type Server struct {
	listener        *net.Listener
	Address         string
	OutputDirectory string
	streamHandler   func(hostname string, file string) string
	streams         []ServerLogStream
}

// ServerLogStream handles a log stream
type ServerLogStream struct {
	*LogStream
	server    *Server
	localFile *os.File
}

// Prometheus counters
var (
	metricClientsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loghamsterd_active_clients",
			Help: "Number of connected and active clients",
		},
		[]string{},
	)
	metricClientsConnected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loghamsterd_connected_clients",
			Help: "Number of connected (active and idle) clients",
		},
		[]string{},
	)
	metricClientConnectsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loghamsterd_client_connects_total",
			Help: "Number of connected (active and idle) clients",
		},
		[]string{},
	)
	metricClientDisconnectsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loghamsterd_client_disconnects_total",
			Help: "Number of connected (active and idle) clients",
		},
		[]string{},
	)
	metricBytesRecvTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loghamster_bytes_received_total",
			Help: "Number of connected (active and idle) clients",
		},
		[]string{},
	)
	metricBytesRecv = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loghamster_bytes_received",
			Help: "Number of bytes received for named connection",
		},
		[]string{"name"},
	)
)

// ListenPrometheus will provide application metrics via HTTP under e/metrics
func ListenPrometheus(listen string) {
	// Register the summary and the histogram with Prometheus's default registry.
	prometheus.MustRegister(metricClientsActive)
	prometheus.MustRegister(metricClientsConnected)
	prometheus.MustRegister(metricBytesRecvTotal)

	log.Debug().Str("listen", listen).Msg("Starting prometheus metrics provider")
	http.Handle("/metrics", promhttp.Handler())
	err := http.ListenAndServe(listen, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to listen for prometheus HTTP")
	} else {
		log.Info().Str("listen", listen).Msg("Listen for prometheus metrics on ")
	}
}

// NewServer initiates a new client connection
func NewServer(address string, directory string, handler func(string)) (*Server, error) {

	log.Info().Str("listen", address).Msg("Attempt to listen")
	// connect to this socket
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Error().Err(err).Msg("Failed to listen at")
		return nil, err
	}

	log.Info().Interface("listener", l).Msg("Accept connections now")

	server := Server{&l, address, directory, nil, nil}
	go server.acceptConnections(l)
	return &server, err
}

func (server Server) acceptConnections(l net.Listener) error {
	for {
		log.Info().Interface("listener", l).Msg("Waiting for new connections")
		conn, err := l.Accept()
		if err != nil {
			log.Error().Err(err).Msg("Failed to accept connections")
			return err
		}

		metricClientsConnected.WithLabelValues().Inc()
		metricClientConnectsTotal.WithLabelValues().Inc()

		streamID := generateStreamID()
		stream := ServerLogStream{&LogStream{conn, streamID, "", ""}, &server, nil}
		s := append(server.streams, stream)
		log.Debug().Interface("stream", stream).Msg("Accepted connection, adding stream ")
		server.streams = s
		stream.writeMessage("# Welcome to LogHamster v" + Version)
		stream.writeMessage("STREAMID " + stream.streamID)
		go stream.handleCommands()
	}
}

// findStream will search for a stream in streams list
func (server Server) findStream(streamID string) *ServerLogStream {
	var stream *ServerLogStream
	streams := server.streams
	for _, s := range streams {
		if s.streamID == streamID {
			stream = &s
			break
		}
	}
	return stream
}

// handleCommand will wait and handle new commands
func (stream ServerLogStream) handleCommands() {
	cmdIdx := 0
	for {
		log.Info().Msg("Await next command")
		line, err := stream.awaitMessage()
		if err != nil {
			log.Error().Err(err).Str("stream", stream.streamID).Msg("Failed to await message from stream")
			break
		}
		if line == "" || line == "\n" {
			continue
		}
		cmds := strings.Split(strings.Replace(strings.TrimSpace(line), ",", " ", -1), " ")
		cmd := cmds[0]
		args := cmds[1:]
		log.Debug().Int("idx", cmdIdx).Str("cmd", cmd).Msg("Process command ")
		switch cmd {
		case "INIT":
			log.Debug().Str("line", line).Msg("Init logstream")
			// Format: INIT STREAM host:/path/file srv:service more:meta
			if len(args) < 2 {
				stream.writeMessage("ERR 500 Missing arguments for " + cmd)
				continue
			}
			params := strings.Split(args[1], ":")
			host, file := params[0], params[1]
			log.Info().Str("host", host).Str("file", file).Msg("Using hostname/file")
			// Based on hostname/filename a output configuration must be detected
			err := stream.initStreamSink(host, file)
			if err != nil {
				log.Error().Err(err).Str("stream", stream.streamID).Str("host", host).Str("file", file).Msg("Failed to init stream")
				continue
			}
			if stream.localFile == nil {
				log.Error().Err(err).Str("stream", stream.streamID).Str("host", host).Str("file", file).Msg("Failed to init stream, no file to stream to")
				continue
			}
			stream.writeMessage(fmt.Sprintf("OK %s %d", stream.streamID, cmdIdx))
			if err != nil {
				log.Info().Msg("[ERROR] During writeMessage to client, aborting")
				continue
			}
			log.Info().Str("stream", stream.streamID).Str("localfile", stream.localFile.Name()).Msg("Streaming data to file")
			metricClientsActive.WithLabelValues().Inc()
			n, err := stream.copyStream()
			log.Info().Err(err).Str("stream", stream.streamID).Int64("count", n).Msg("Stream completed")
			if err != nil {
				if err == io.EOF {
					log.Debug().Err(err).Str("stream", stream.streamID).Msg("EOF reached for stream")
				} else {
					stream.writeMessage("ERR 500 Failed after " + string(n) + " bytes from stream" + stream.streamID)
				}
			} else {
				stream.writeMessage(fmt.Sprintf("OK %d %d", cmdIdx, n))
			}
		case "CLOSE":
			log.Info().Str("stream", stream.streamID).Msg("Close logstream")
			stream.writeMessage(fmt.Sprintf("OK %d", cmdIdx))
			stream.Close()
			return
		default:
			stream.writeMessage("ERR 500 Unknown command" + cmd)
		}
		cmdIdx = cmdIdx + 1
	}
}

func generateStreamID() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("abcdef1234567890")
	length := 6
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}

func ensureDir(fileName string) {
	dirName := filepath.Dir(fileName)
	if _, serr := os.Stat(dirName); serr != nil {
		merr := os.MkdirAll(dirName, os.ModePerm)
		if merr != nil {
			panic(merr)
		}
	}
}

//initStreamSink initiates a new log stream
func (stream *ServerLogStream) initStreamSink(hostname string, file string) error {
	// Map to logfile now and open it for writing
	// TODO: use correct filename from mapping or deny init
	var localfile string
	if stream.server.streamHandler != nil {
		localfile = stream.server.streamHandler(hostname, file)
		log.Info().Msgf("Using configured output file for %s:%s from config: %s", hostname, file, localfile)
	} else {
		directory := stream.server.OutputDirectory
		localfile = fmt.Sprintf("%s/%s/%s.out.log", directory, hostname, strings.Trim(strings.Replace(file, "/", "_", -1), "_/"))
		log.Info().Msgf("Initialized stream sink for %s:%s using default mapping: %s", hostname, file, localfile)
	}
	ensureDir(localfile)
	f, err := os.OpenFile(localfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	if err != nil {
		log.Error().Err(err).Str("localfile", localfile).Msg("Failed to open file for writing")
	}
	stream.localFile = f
	return err
}

func (stream ServerLogStream) copyStream() (int64, error) {
	conn := stream.conn
	file := stream.localFile
	bufsize := int64(defaultBuffersize)
	total := int64(0)
	retry := 0
	for {
		n, err := io.CopyN(file, conn, bufsize)
		total = total + n
		metricBytesRecvTotal.WithLabelValues().Add(float64(n))
		metricBytesRecv.WithLabelValues(stream.filename).Add(float64(n))
		log.Debug().Str("stream", stream.streamID).Str("file", file.Name()).Int64("read", n).Int64("total", total).Msg("Read from stream to local file")
		if err != nil {
			if err == io.EOF {
				file.Sync()
				retry = retry + 1
				if retry < 2 {
					log.Info().Str("stream", stream.streamID).Int("retry", retry).Msg("Retry reading/writing after 1 second")
					time.Sleep(1 * time.Second)
					continue
				}
				log.Info().Msg("EOF reached")
				break
			} else {
				log.Error().Err(err).Str("stream", stream.streamID).Int64("bufsize", bufsize).Int64("count", n).Msg("Failed to read buffer")
				return total, err
			}
		}
		retry = 0
	}

	log.Info().Str("stream", stream.streamID).Str("file", file.Name()).Int64("total", total).Msg("Stream completed")

	file.Sync()
	return total, nil
}
