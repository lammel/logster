package logster

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Server handles a logster client connection
type Server struct {
	listener *net.Listener
	address  string
	streams  []ServerLogStream
}

// LogStream handles a log stream
type LogStream struct {
	conn     net.Conn
	streamID string
	hostname string
	filename string
}

// ServerLogStream handles a log stream
type ServerLogStream struct {
	LogStream
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
		stream, _ := server.initLogStream(conn, "dummy", "tester")
		s := append(server.streams, stream)
		log.Println("[DBG] Adding stream ", stream)
		server.streams = s
		stream.writeResponse("OK STREAM " + stream.streamID)
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

// writeCommand will write a single command to the server
func (stream LogStream) writeResponse(message string) error {
	log.Println("[DBG] Writing message", message)
	conn := stream.conn
	writer := bufio.NewWriter(conn)
	int, err := writer.WriteString(message + "\n")
	log.Println("[DBG] Wrote", int, "bytes on connection")
	writer.Flush()
	return err
}

// awaitCommand will wait for a new command
func (stream ServerLogStream) awaitCommand() (string, error) {
	conn := stream.conn
	reader := bufio.NewReader(conn)
	line, _, err := reader.ReadLine()
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
	}
	return string(line), err
}

// handleCommand will wait and handle new commands
func (stream ServerLogStream) handleCommands() {
	for {
		log.Println("[DBG] Await next command")
		line, err := stream.awaitCommand()
		if err != nil {
			log.Println("[ERROR] Error for awaitCommand")
			break
		}
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, CommandPrefix) {
			log.Println("[WARN] Ignoring invalid command line:", line)
			stream.writeResponse("ERR 500 Invalid command")
			continue
		}
		cmds := strings.Split(strings.Replace(strings.TrimLeft(line, CommandPrefix), ",", " ", -1), " ")
		cmd := cmds[0]
		args := cmds[1:]
		switch cmd {
		case "INIT":
			log.Println("[DBG] Init logstream", line)
			if len(args) < 2 {
				stream.writeResponse("ERR 500 Missing arguments for " + cmd)
				continue
			}
			stream.hostname = args[0]
			stream.filename = args[1]
			log.Println("[INFO] Using hostname", stream.hostname, "filename", stream.filename)
			stream.writeResponse("OK " + stream.streamID)
		case "SEND":
			log.Println("Send to logstream", line)
			if len(args) < 1 {
				stream.writeResponse("ERR 500 Missing arguments for " + cmd)
				continue
			}
			log.Println("[DBG] Receiving on stream now")
			length, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				stream.writeResponse("ERR 500 Invalid size for stream " + args[0])
				break
			}
			n, err := stream.copy(length)
			if err != nil {
				stream.writeResponse("ERR 500 Failed to find stream " + args[0])
			} else {
				stream.writeResponse(fmt.Sprintf("OK %d", n))
			}
		case "CLOSE":
			log.Println("Close logstream", stream.streamID)
			stream.writeResponse("OK")
			stream.close()
			return
		default:
			stream.writeResponse("ERR 500 Unknown command" + cmd)
		}
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

// initLogStream initiates a new log stream
func (server *Server) initLogStream(conn net.Conn, hostname string, file string) (ServerLogStream, error) {
	streamID := generateStreamID()
	// Map to logfile now and open it for writing
	// TODO: use correct filename from mapping or deny init
	localfile := "/tmp/logster.dummy.log"
	// f, err := os.OpenFile(localfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	f, err := os.Create(localfile)
	if err != nil {
		log.Println("Failed to open", localfile, "for writing:", err)
	}

	stream := ServerLogStream{LogStream{conn, streamID, "", ""}, server, f}
	return stream, err
}

func (stream ServerLogStream) recv(len int64) error {
	conn := stream.conn
	// write data to connection now
	reader := bufio.NewReader(conn)
	buf := make([]byte, len)
	rcvd, err := reader.Read(buf)
	log.Println("Read", rcvd, "of", len, "bytes from stream")
	if err != nil {
		log.Println("Error during read of", len, " bytes:", err)
		stream.writeResponse(fmt.Sprintf("%sERR %d %s", CommandPrefix, 500, "Failed to read"))
		return err
	}

	log.Println("Success for recv with", rcvd, "bytes")
	f := *stream.localFile
	n, err := f.Write(buf)
	if err != nil {
		log.Println("Failed to write to local file", f.Name(), "(", n, " bytes written)", err)
		stream.writeResponse(fmt.Sprintf("%sERR %d %s", CommandPrefix, n, "Failed to write"))
		return err
	}

	log.Println("Wrote", n, "bytes to", f.Name())
	stream.writeResponse(fmt.Sprintf("%sOK %d", CommandPrefix, n))

	return nil
}

func (stream ServerLogStream) copy(len int64) (int64, error) {
	conn := stream.conn
	file := stream.localFile
	// write data to connection now
	// reader := bufio.NewReader(conn)
	// writer := bufio.NewWriter(file)
	log.Println("[DBG] Copy", len, "bytes to file", file.Name())
	n, err := io.CopyN(file, conn, int64(len))
	if err != nil {
		log.Println("Error during read of", len, " bytes:", err)
		stream.writeResponse(fmt.Sprintf("%sERR %d %s", CommandPrefix, 500, "Failed to read"))
		return 0, err
	}

	log.Println("[DBG] Wrote", n, "bytes to", file.Name())
	return n, nil
}

func (stream ServerLogStream) close() {
	log.Println("[INFO] Closing connection", stream.streamID)
	conn := stream.conn
	err := conn.Close()
	if err != nil {
		log.Println("Error during close of stream", stream.streamID)
	} else {
		log.Println("Successfully closed stream", stream.streamID)
	}
}
