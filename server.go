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
		stream.writeResponse("OK " + stream.streamID)
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
	n, err := writer.WriteString(message + "\n")
	writer.Flush()
	log.Println("[DBG] Wrote response", message, "with", n, "bytes on stream", stream.streamID)
	return err
}

// awaitCommand will wait for a new command
func (stream ServerLogStream) awaitCommand() (string, error) {
	conn := stream.conn
	reader := bufio.NewReader(conn)
	log.Println("[DBG] Reading await command on stream", stream.streamID)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Println("ERROR on awaitCommand with line", line, ":", err)
		return "", err
	}
	log.Println("Read: '" + line + "'")
	return string(line), nil
}

// handleCommand will wait and handle new commands
func (stream ServerLogStream) handleCommands() {
	cmdIdx := 0
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
		cmds := strings.Split(strings.Replace(strings.TrimLeft(strings.TrimSpace(line), CommandPrefix), ",", " ", -1), " ")
		cmd := cmds[0]
		args := cmds[1:]
		log.Println("[DBG] Process command ", cmdIdx, cmd)
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
			stream.writeResponse("OK")
			stream.writeResponse(fmt.Sprintf("OK %s %d", stream.streamID, cmdIdx))

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
				stream.writeResponse("ERR 500 Failed to read " + args[0] + " bytes from stream")
			} else {
				stream.writeResponse(fmt.Sprintf("OK %d %d", cmdIdx, n))
			}
		case "STREAM":
			log.Println("[INFO] Starting streaming of data", line)
			err := stream.writeResponse(fmt.Sprintf("OK %d", cmdIdx))
			if err != nil {
				log.Println("[ERROR] During WriteResponse")
			}
			log.Println("Starting stream now")
			n, err := stream.copyStream()
			log.Println("Stream", stream.streamID, "ending after", n, "bytes:", err)
			if err != nil {
				if err == io.EOF {
					log.Println("End of stream", err)
				} else {
					stream.writeResponse("ERR 500 Failed after " + string(n) + " bytes from stream" + stream.streamID)
				}
			} else {
				stream.writeResponse(fmt.Sprintf("OK %d %d", cmdIdx, n))
			}
		case "CLOSE":
			log.Println("Close logstream", stream.streamID)
			stream.writeResponse(fmt.Sprintf("OK %d", cmdIdx))
			stream.close()
			return
		default:
			stream.writeResponse("ERR 500 Unknown command" + cmd)
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
	//timeoutDuration := 5 * time.Second
	//	conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	buf := make([]byte, len)
	reader := io.LimitReader(conn, len)
	n, err := reader.Read(buf)
	log.Println("Read:", string(buf), "with", n, "bytes")
	if err != nil {
		log.Println("Could not read full len", len, "from connection", conn)
		return int64(n), err
	}
	n, err = file.Write(buf)
	// n, err := io.CopyN(file, conn, len)
	//	conn.SetReadDeadline(time.Unix(0, 0))
	if err != nil {
		log.Println("Error during read of", len, " bytes (received", n, " bytes):", err)
		return 0, err
	}
	log.Println("[DBG] Wrote", n, "bytes to", file.Name())
	file.Sync()
	return int64(n), nil
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
