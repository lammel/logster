package logster

import (
	"bufio"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

const (
	// Version string for application
	Version string = "0.1"

	// Buffersize used for internal streaming to/from file
	defaultBuffersize int = 4096
)

// LogStream handles a log stream
type LogStream struct {
	conn     net.Conn
	streamID string
	hostname string
	filename string
}

// LogStreamInterface interface
type LogStreamInterface interface {
	Reader() io.Reader
	Writer() io.Writer
	Reconnect()
	writeMessage()
	awaitResponse()
}

// Reconnect the underlying connection
func (stream *LogStream) Reconnect(server string) error {
	log.Println("[DBG] Reconnecting stream", stream.streamID)
	stream.conn.Close()
	time.Sleep(2 * time.Second)
	conn, err := net.Dial("tcp", server)
	if err != nil {
		log.Println("[ERROR] Unable to reconnect")
		return err
	}
	stream.conn = conn
	return nil
}

func (stream ServerLogStream) close() {
	log.Println("[DBG] Closing connection", stream.streamID)
	err := stream.conn.Close()
	if err != nil {
		log.Println("[ERROR] Failed to close stream", stream.streamID, err)
	} else {
		log.Println("[INFO] Successfully closed stream", stream.streamID)
	}
}

// writeMessage will write a single command to the server
func (stream LogStream) writeMessage(msg string) error {
	conn := stream.conn
	writer := bufio.NewWriter(conn)
	// send to socket
	const timeoutDuration = 5 * time.Second
	// conn.SetWriteDeadline(time.Now().Add(timeoutDuration))
	n, err := writer.WriteString(msg + "\n")
	writer.Flush()
	// conn.SetWriteDeadline(time.Unix(0, 0))
	log.Println("[DBG] Wrote message", msg, "with", n, "bytes to stream", stream.streamID)
	return err
}

// awaitMessage will write a single command to the server
func (stream LogStream) awaitMessage() (string, error) {
	conn := stream.conn
	reader := bufio.NewReader(conn)
	log.Println("[DBG] Reading and awaiting message on stream", stream.streamID)
	const timeoutDuration = 5 * time.Second
	// conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	line, err := reader.ReadString('\n')
	// conn.SetReadDeadline(time.Unix(0, 0))
	if err != nil {
		log.Println("Error on awaitMessage for stream", stream.streamID, ":", err)
		if strings.Contains(err.Error(), "timeout") {
			log.Println("Timeout detected for stream", stream.streamID)
		}
		if strings.Contains(err.Error(), "closed") {
			log.Println("Closed connection detected for stream", stream.streamID)
		}
		stream.conn.Close()
	}
	return string(line), err
}
