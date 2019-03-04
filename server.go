package logster

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"
)

// Server handles a logster client connection
type Server struct {
	listener *net.Listener
	address  string
	streams  []ServerLogStream
}

// ServerLogStream handles a log stream
type ServerLogStream struct {
	*LogStream
	server    *Server
	localFile *os.File
}

// NewServer initiates a new client connection
func NewServer(address string) (*Server, error) {

	log.Println("Attempt to listen at", address)
	// connect to this socket
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Println("Failed to listen at", err)
		return nil, err
	}

	log.Println("Accept connections now", l)

	server := Server{&l, address, nil}
	go server.acceptConnections(l)
	return &server, err
}

func (server Server) acceptConnections(l net.Listener) error {
	for {
		log.Println("Waiting for new connections ", l)
		conn, err := l.Accept()
		if err != nil {
			log.Println("Failed to accept connections", err)
			return err
		}

		streamID := generateStreamID()
		stream := ServerLogStream{&LogStream{conn, streamID, "", ""}, &server, nil}
		s := append(server.streams, stream)
		log.Println("[DBG] Accepted connection, adding stream ", stream)
		server.streams = s
		stream.writeMessage("# Welcome to Logster v" + Version)
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
		log.Println("[DBG] Await next command")
		line, err := stream.awaitMessage()
		if err != nil {
			log.Println("[ERROR] Failed to await message from stream", stream.streamID, ":", err)
			break
		}
		if line == "" || line == "\n" {
			continue
		}
		cmds := strings.Split(strings.Replace(strings.TrimSpace(line), ",", " ", -1), " ")
		cmd := cmds[0]
		args := cmds[1:]
		log.Println("[DBG] Process command ", cmdIdx, cmd)
		switch cmd {
		case "INIT":
			log.Println("[DBG] Init logstream", line)
			// Format: INIT STREAM host:/path/file srv:service more:meta
			if len(args) < 2 {
				stream.writeMessage("ERR 500 Missing arguments for " + cmd)
				continue
			}
			params := strings.Split(args[1], ":")
			host, file := params[0], params[1]
			log.Println("[INFO] Using hostname", host, "filename", file)
			// Based on hostname/filename a output configuration must be detected
			err := stream.initStreamSink(host, file)
			if err != nil {
				log.Println("[ERROR] Failed to init stream for host", host, "file", file, ":", err)
				continue
			}
			if stream.localFile == nil {
				log.Println("[ERROR] No file to stream to, aborting. host", host, "file", file, ":")
				continue
			}
			stream.writeMessage(fmt.Sprintf("OK %s %d", stream.streamID, cmdIdx))
			if err != nil {
				log.Println("[ERROR] During writeMessage to client, aborting")
				continue
			}
			log.Println("[INFO] Streaming data to file", stream.localFile, "for stream", stream.streamID)
			n, err := stream.copyStream()
			log.Println("Stream", stream.streamID, "ending after", n, "bytes:", err)
			if err != nil {
				if err == io.EOF {
					log.Println("EOF reached for stream", err)
				} else {
					stream.writeMessage("ERR 500 Failed after " + string(n) + " bytes from stream" + stream.streamID)
				}
			} else {
				stream.writeMessage(fmt.Sprintf("OK %d %d", cmdIdx, n))
			}
		case "CLOSE":
			log.Println("Close logstream", stream.streamID)
			stream.writeMessage(fmt.Sprintf("OK %d", cmdIdx))
			stream.close()
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

//initStreamSink initiates a new log stream
func (stream *ServerLogStream) initStreamSink(hostname string, file string) error {
	// Map to logfile now and open it for writing
	// TODO: use correct filename from mapping or deny init
	localfile := fmt.Sprintf("%s/%s_%s.out.log", "/tmp", hostname, strings.Replace(file, "/", "_", -1))
	// f, err := os.OpenFile(localfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	f, err := os.Create(localfile)
	if err != nil {
		log.Println("Failed to open", localfile, "for writing:", err)
	}
	stream.localFile = f
	return err
}

func (stream ServerLogStream) copyStream() (int64, error) {
	conn := stream.conn
	file := stream.localFile
	bufsize := int64(512)
	total := int64(0)
	retry := 0
	for {
		n, err := io.CopyN(file, conn, bufsize)
		total = total + n
		log.Println("Read", total, "bytes total for stream", stream.streamID, "to local file", file.Name())
		if err != nil {
			if err == io.EOF {
				retry = retry + 1
				if retry < 2 {
					log.Println("Retry", retry, "reading/writing after 1 second")
					time.Sleep(1 * time.Second)
					continue
				}
				log.Println("EOF reached")
				break
			} else {
				log.Println("[ERROR] Error during read of buffer with", bufsize, "bytes (received", n, " bytes):", err)
				return total, err
			}
		}
		retry = 0
	}

	log.Println("[DBG] Wrote", total, "bytes to", file.Name())
	file.Sync()
	return total, nil
}
