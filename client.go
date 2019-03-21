package loghamster

import (
	"fmt"
	"io"

	"net"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Client handles a loghamster client connection
type Client struct {
	server  string
	streams []*ClientLogStream
}

// ClientLogStream handles a log stream
type ClientLogStream struct {
	*LogStream
	server string
}

// NewClient initiates a new client connection
func NewClient(server string) *Client {
	streams := []*ClientLogStream{nil}
	client := Client{server, streams}
	return &client
}

// NewLogStream initiates a new log stream
func (client *Client) NewLogStream(hostname string, file string) (*ClientLogStream, error) {
	stream := NewLogStream(client.server, hostname, file)
	stream.Connect()

	client.addStream(&stream)
	return &stream, nil
}

// CloseLogStream closes a log stream
func (client *Client) CloseLogStream(stream *ClientLogStream) {
	stream.conn.Close()
	client.removeStream(stream)
}

// AddStream will add a ClientLogStream to the list of monitored streams
func (client *Client) addStream(stream *ClientLogStream) error {
	client.streams = append(client.streams, stream)
	log.Debug().Str("stream", stream.streamID).Int("count", len(client.streams)).Str("server", stream.server).Msg("Added log stream to monitored streams")
	return nil
}

// RemoveStream will search for a stream in streams list
func (client *Client) removeStream(stream *ClientLogStream) error {
	removed := false
	for i, s := range client.streams {
		if s == stream {
			// Remove from list of active streams
			client.streams = append(client.streams[:i], client.streams[i+1:]...)
			log.Debug().Str("stream", stream.streamID).Int("count", len(client.streams)).Str("server", stream.server).Msg("Removed log stream to monitored streams")
			removed = true
		}
	}
	if !removed {
		log.Warn().Str("stream", stream.streamID).Str("path", stream.filename).Msg("Could not find stream to remove")
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

// NewLogStream stream
func NewLogStream(server, hostname, filename string) ClientLogStream {
	source := LogStream{nil, "", hostname, filename}
	s := ClientLogStream{&source, server}
	return s
}

// Connect the stream
func (stream *ClientLogStream) Connect() error {
	// connect to this socket
	conn, err := net.Dial("tcp", stream.server)
	if err != nil {
		log.Error().Err(err).Str("server", stream.server).Msg("Failed to connect to server")
		return err
	}
	stream.conn = conn

	line, err := stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Failed to await Hello from server. Aborting")
		stream.Close()
		return err
	}
	log.Info().Str("server", line).Msg("Connected to server")
	line, err = stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Msg("Failed to await StreamID from server. Aborting")
		stream.Close()
		return err
	}
	resp := strings.Split(strings.Trim(line, "\n"), " ")
	log.Debug().Str("response", line).Msg("Response")
	if resp[0] != "STREAMID" {
		log.Info().Msg("Could not identify stream ID, aborting")
		stream.Close()
		return err
	}
	stream.streamID = resp[1]
	log.Debug().Str("stream", stream.streamID).Msg("Received streamID from server")

	stream.writeMessage(fmt.Sprintf("INIT STREAM %s:%s", stream.hostname, stream.filename))
	line, err = stream.awaitMessage()
	if err != nil {
		log.Error().Err(err).Str("stream", stream.streamID).Msg("ERROR on awaitResponse:")
		if strings.Contains(err.Error(), "timeout") {
			log.Error().Err(err).Msg("Timeout detected on stream")
			stream.Close()
		}
		return err
	}
	log.Debug().Str("stream", stream.streamID).Str("line", line).Msg("Response")
	if !strings.HasPrefix(line, "OK") {
		log.Info().Msg("Failed to init stream")
		stream.Close()
	}
	return nil
}

// Reconnect the underlying connection
func (stream *ClientLogStream) Reconnect() error {
	log.Debug().Str("stream", stream.streamID).Msg("Reconnecting stream, closing and reconnecting")
	stream.Close()
	time.Sleep(1 * time.Second)
	log.Info().Str("stream", stream.filename).Msg("Reconnecting stream for path")
	err := stream.Connect()
	if err != nil {
		log.Error().Err(err).Msg("Unable to reconnect")
		return err
	}
	log.Debug().Str("stream", stream.streamID).Msg("Reconnected stream")
	return nil
}

func closeFile(file *os.File) {
	log.Info().Msg("Closing file")
	file.Close()
}

// StreamFile will search for a stream in streams list
func (stream *ClientLogStream) StreamFile(path string, lastPos int64) (int64, error) {
	total := int64(0)
	pos := lastPos
	var lastErr error
	var file *os.File
	var err error
	retry := 0
	const maxDelay = 30

	for {
		log.Info().Msg("Starting loop for stream file data")
		if stream.conn == nil {
			log.Warn().Msg("No valid connection available, connecting...")
			err := stream.Reconnect()
			if err != nil {
				log.Error().Err(err).Str("server", stream.server).Msg("Failed to connect initially")
			}
		}
		if file == nil {
			file, err = os.Open(path)
			defer closeFile(file)
		}
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("Unable to open file")
			return total, err
		}
		info, _ := file.Stat()
		if info.Size() < pos {
			log.Info().Int64("pos", pos).Int64("size", info.Size()).Msg("Last position greater than file size. Starting from beginning")
			pos = 0
		}
		if stream.conn != nil {
			n, err := stream.streamFileData(file, pos)
			total = total + n
			pos = pos + n
			log.Debug().Err(err).Int64("read", n).Int64("pos", pos).Int64("size", info.Size()).Msg("Stream data completed")
		}

		if err != nil {
			log.Warn().Err(err).Int("retry", retry).Msg("Error during stream data")
			retry = retry + 1
			if err == io.ErrClosedPipe || strings.Contains(err.Error(), " connection refused") ||
				strings.Contains(err.Error(), " not accepted") || strings.Contains(err.Error(), " broken pipe") {
				delay := retry * 2
				if delay > maxDelay {
					delay = maxDelay
				}

				log.Error().Err(err).Msgf("Waiting %d seconds before reconnect", delay)
				time.Sleep(time.Duration(delay))
				err := stream.Reconnect()
				if err != nil {
					log.Error().Err(err).Msg("Error during reconnect")
				}
				continue
			} else {
				log.Error().Err(err).Msg("StreamFile returned error")
				lastErr = err
				break
			}
		} else {
			log.Warn().Str("path", path).Msg("No action")
			retry = 0
			time.Sleep(1 * time.Second)
		}

	}

	return total, lastErr
}

// StreamFile will search for a stream in streams list
func (stream *ClientLogStream) streamFileData(file *os.File, lastPos int64) (int64, error) {
	log.Info().Str("path", file.Name()).Int64("pos", lastPos).Msg("Stream file from position")
	file.Seek(lastPos, 0)
	total := int64(0)
	bufsize := int64(defaultBuffersize)
	lastRead := time.Now()
	// buf := make([]byte, defaultBuffersize)
	for {
		log.Info().Str("path", file.Name()).Int64("pos", lastPos+total).Msg("Sending data from pos")
		n, err := io.CopyN(stream.conn, file, bufsize)
		if n > 0 {
			log.Info().Err(err).Int64("n", n).Msg("Copy stream returned")
			lastRead = time.Now()
		}
		if err != nil {
			if err == io.EOF {
				total = total + n
				idle := time.Now().Sub(lastRead)
				if idle > 10*time.Second {
					log.Debug().Str("path", file.Name()).Dur("idle", idle).Msg("Idle file. Increasing interval")
					time.Sleep(10 * time.Second)
				} else {
					time.Sleep(1 * time.Second)
				}
				continue
			}
			log.Error().Err(err).Str("path", file.Name()).Msg("Error during stream data")
			stream.Close()
			// Strange workaround to restore correct position, to avoid sending too few data
			if (err == io.ErrClosedPipe || err == io.ErrUnexpectedEOF || strings.Contains(err.Error(), "broken pipe")) && total > int64(defaultBuffersize) {
				log.Warn().Msg("Apply workaround to avoid wrong position")
				total = total - int64(defaultBuffersize)
			}
			return total, err
		}
		if n > 0 {
			// log.Info().Err(err).Int64("n", n).Str("buf", string(buf)).Msg("Copy stream returned")
			log.Info().Str("stream", stream.streamID).Str("path", file.Name()).Int64("count", n).Int64("total", total).Msg("Sent data to stream")
			total = total + n
		} else {
			time.Sleep(1 * time.Second)
		}
	}
	return total, nil
}

// CloseGraceful will close the stream
func (stream *ClientLogStream) CloseGraceful() {
	if stream.conn != nil {
		stream.writeMessage(fmt.Sprintf("CLOSE %s", stream.streamID))
		line, err := stream.awaitMessage()
		if err != nil {
			log.Error().Err(err).Msg("Error during close")
		} else {
			log.Debug().Str("line", line).Msg("Response:")
			if strings.HasPrefix(line, "OK") {
				log.Info().Msg("Close acknowledged by server")
			}
		}
		stream.Close()
	}
}

// Close will close the stream
func (stream *ClientLogStream) Close() {
	if stream.conn != nil {
		log.Info().Msgf("Closing stream %s", stream.streamID)
		stream.conn.Close()
		stream.conn = nil
	} else {
		log.Debug().Msgf("Stream %s already closed", stream.streamID)
	}
}
