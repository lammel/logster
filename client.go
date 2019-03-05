package logster

import (
	"bufio"
	"fmt"
	"io"

	"net"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Client handles a logster client connection
type Client struct {
	server  string
	streams []*ClientLogStream
}

// ClientLogStream handles a log stream
type ClientLogStream struct {
	*LogStream
	client *Client
}

// NewClient initiates a new client connection
func NewClient(server string) *Client {
	streams := []*ClientLogStream{nil}
	client := Client{server, streams}
	return &client
}

// NewLogStream initiates a new log stream
func (client *Client) NewLogStream(hostname string, file string) (*ClientLogStream, error) {
	// connect to this socket
	conn, err := net.Dial("tcp", client.server)
	if err != nil {
		log.Error().Err(err).Str("server", client.server).Msg("Failed to connect to server")
		return nil, err
	}

	source := LogStream{conn, "", hostname, file}
	stream := ClientLogStream{&source, client}

	line, err := stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Failed to await Hello from server. Aborting")
		stream.Close()
		return nil, err
	}
	log.Info().Str("server", line).Msg("Connected to server")
	line, err = stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Failed to await StreamID from server. Aborting")
		stream.Close()
		return nil, err
	}
	resp := strings.Split(strings.Trim(line, "\n"), " ")
	log.Debug().Str("response", line).Msg("Response")
	if resp[0] != "STREAMID" {
		log.Info().Msg("Could not identify stream ID, aborting")
		stream.Close()
		return nil, err
	}
	stream.streamID = resp[1]
	log.Debug().Str("stream", stream.streamID).Msg("Received streamID from server")

	stream.writeMessage(fmt.Sprintf("INIT STREAM %s:%s", hostname, file))
	line, err = stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Str("stream", stream.streamID).Msg("ERROR on awaitResponse:")
		if strings.Contains(err.Error(), "timeout") {
			log.Error().Err(err).Msg("Timeout detected on stream")
			stream.Close()
		}
		return nil, err
	}
	log.Debug().Str("stream", stream.streamID).Str("line", line).Msg("Response")
	if !strings.HasPrefix(line, "OK") {
		log.Info().Msg("Failed to init stream")
		stream.Close()
	}

	s := append(client.streams, &stream)
	log.Debug().Str("stream", stream.streamID).Str("server", line).Msg("Adding new log stream")
	client.streams = s

	return &stream, nil
}

// CloseLogStream closes a log stream
func (client *Client) CloseLogStream(stream *ClientLogStream) {
	stream.conn.Close()
	for i, s := range client.streams {
		if s == stream {
			// Remove from list of active streams
			client.streams = append(client.streams[:i], client.streams[i+1:]...)
		}
	}
}

// SendToStreamByPath will search for a stream in streams list
func (client *Client) SendToStreamByPath(path string, data string) error {
	stream := client.FindStreamByPath(path)
	if stream != nil {
		err := stream.Send(data)
		if err != nil {
			log.Info().Msg("Error during send")
			if strings.Contains(err.Error(), "timeout") {
				log.Info().Msg("Timeout on stream detected, recreating stream")
			}
			client.CloseLogStream(stream)
			newstream, _ := client.NewLogStream(stream.hostname, stream.filename)
			err = newstream.Send(data)

		}
	} else {
		log.Info().Str("path", path).Msg("Unable to find stream for path")
	}
	return nil
}

// FindStreamByPath will search for a stream in streams list
func (client *Client) FindStreamByPath(path string) *ClientLogStream {
	var stream *ClientLogStream
	streams := client.streams
	for _, s := range streams {
		if s == nil {
			log.Debug().Interface("stream", s).Msg("Skipping nil stream")
			continue
		}
		if s.filename == path {
			stream = s
			break
		}
	}
	return stream
}

// FindStreamByID will search for a stream in streams list
func (client *Client) FindStreamByID(streamID string) *ClientLogStream {
	var stream *ClientLogStream
	streams := client.streams
	for _, s := range streams {
		if s.streamID == streamID {
			stream = s
			break
		}
	}
	return stream
}

// StreamFileHandler will search for a stream in streams list
func (stream *ClientLogStream) StreamFileHandler(path string, lastPos int64) (int64, error) {
	total := int64(0)
	pos := lastPos
	var lastErr error
	retry := 0

	for {
		file, err := os.Open(path)
		defer file.Close()
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("Unable to open file")
			return total, err
		}
		info, _ := file.Stat()
		if info.Size() < pos {
			log.Info().Int64("pos", pos).Int64("size", info.Size()).Msg("Last position greater than file size. Starting from beginning")
			pos = 0
		}
		time.Sleep(1 * time.Second)
		n, err := stream.StreamFile(file, pos)
		total = total + n
		pos = pos + n
		log.Debug().Int64("read", n).Int64("pos", pos).Int64("size", info.Size()).Msg("Read from file")

		if err != nil {
			retry = retry + 1
			if strings.Contains(err.Error(), " not accepted") {
				if retry > 3 {
					err := stream.Reconnect(stream.client.server)
					if err != nil {
						log.Info().Msg("Error during reconnect")
					}
				}
			} else {
				log.Error().Err(err).Msg("StreamFile returned error")
				lastErr = err
				break
			}
		}
		retry = 0
	}

	return total, lastErr
}

// StreamFile will search for a stream in streams list
func (stream *ClientLogStream) StreamFile(file *os.File, lastPos int64) (int64, error) {
	log.Info().Str("path", file.Name()).Int64("pos", lastPos).Msg("Stream file from position")
	file.Seek(lastPos, 0)
	total := int64(0)
	bufsize := int64(defaultBuffersize)
	for {
		n, err := io.CopyN(stream.conn, file, bufsize)
		total = total + n
		if n > 0 {
			log.Debug().Str("stream", stream.streamID).Str("path", file.Name()).Int64("count", n).Msg("Sent data to stream")
		}
		if err != nil {
			if err == io.EOF {
				log.Debug().Str("path", file.Name()).Msg("End of file (EOF) reached")
				time.Sleep(1 * time.Second)
				continue
			}
			log.Error().Err(err).Str("path", file.Name()).Msg("Error during StreamFile")
			stream.conn.Close()
			return total, err
		}
		log.Debug().Int64("count", n).Msg("Read and written")
	}
	return total, nil
}

// Send will send a batch of stream data
func (stream ClientLogStream) Send(data string) error {
	conn := stream.conn
	// send to socket
	stream.writeMessage(fmt.Sprintf("SEND %d", len(data)))
	// write data to connection now
	writer := bufio.NewWriter(conn)
	len, err := writer.WriteString(data)
	writer.Flush()
	if err != nil {
		log.Error().Err(err).Msg("Error during write")
	}
	log.Debug().Str("stream", stream.streamID).Int("count", len).Msg("Wrote bytes to stream")
	line, err := stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Error during send")
		return err
	}
	log.Debug().Str("line", line).Msg("Response:")
	if strings.HasPrefix(line, "OK") {
		log.Info().Msg("Send OK")
	}
	return nil
}

// Close will close the stream
func (stream ClientLogStream) Close() {
	stream.writeMessage(fmt.Sprintf("CLOSE %s", stream.streamID))
	line, err := stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Error during close")
	} else {
		log.Debug().Str("line", line).Msg("Response:")
		if strings.HasPrefix(line, "OK") {
			log.Info().Msg("Send OK")
		}

	}
}
