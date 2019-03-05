package logster

import (
	"bufio"
	"io"
	"net"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
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
	log.Debug().Str("stream", stream.streamID).Msg("Reconnecting stream")
	stream.conn.Close()
	time.Sleep(2 * time.Second)
	conn, err := net.Dial("tcp", server)
	if err != nil {
		log.Error().Err(err).Msg("Unable to reconnect")
		return err
	}
	stream.conn = conn
	return nil
}

func (stream ServerLogStream) close() {
	log.Debug().Str("stream", stream.streamID).Msg("Closing connection")
	err := stream.conn.Close()
	if err != nil {
		log.Error().Err(err).Str("stream", stream.streamID).Msg("Failed to close stream")
	} else {
		log.Info().Str("stream", stream.streamID).Msg("Successfully closed stream")
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
	log.Debug().Str("stream", stream.streamID).Str("msg", msg).Int("count", n).Msg("Wrote message to stream")
	return err
}

// awaitMessage will write a single command to the server
func (stream LogStream) awaitMessage() (string, error) {
	conn := stream.conn
	reader := bufio.NewReader(conn)
	log.Debug().Str("stream", stream.streamID).Msg("Reading and awaiting message on stream")
	const timeoutDuration = 5 * time.Second
	// conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	line, err := reader.ReadString('\n')
	// conn.SetReadDeadline(time.Unix(0, 0))
	if err != nil {
		log.Debug().Str("stream", stream.streamID).Err(err).Msg("Error on awaitMessage for stream")
		if strings.Contains(err.Error(), "timeout") {
			log.Warn().Str("stream", stream.streamID).Err(err).Msg("Timeout detected for stream")
		}
		if strings.Contains(err.Error(), "closed") {
			log.Warn().Str("stream", stream.streamID).Msg("Closed connection detected for stream")
		}
		stream.conn.Close()
	}
	return string(line), err
}
