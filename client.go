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
	Files   *FileManager
}

// ClientLogStream handles a log stream
type ClientLogStream struct {
	*LogStream
	server    string
	InputFile *os.File
	LastPos   int64
	LastRead  time.Time
}

// NewClient initiates a new client connection
func NewClient(server string, files *FileManager) *Client {
	streams := []*ClientLogStream{}
	client := Client{server, streams, files}
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
	stream.Close()
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
	for i, s := range streams {
		if s == nil {
			log.Debug().Interface("stream", s).Int("idx", i).Msg("Skipping nil stream")
			continue
		}
		if s.filename == path {
			stream = s
			break
		}
	}
	return stream
}

// HandleFileChange shall trigger a stream read/write (read from file write to target)
// TODO: May not be called on every write
func (client *Client) HandleFileChange(path string) error {
	stream := client.FindStreamByPath(path)
	if stream != nil {
		if stream.InputFile == nil {
			stream.OpenInputFile(0)
		}
		if _, err := stream.sendData(); err != nil {
			log.Error().Err(err).Str("stream", stream.streamID).Str("path", stream.filename).Msg("Failed to send data to stream")
			client.CloseLogStream(stream)
		}
	} else {
		if client.Files.FindInputByPath(path) != nil {
			client.NewLogStream(client.server, path)
		}
	}
	return nil
}

// HandleFileCreate shall reopen an existing stream or create a new stream
func (client *Client) HandleFileCreate(path string) error {
	stream := client.FindStreamByPath(path)
	if stream != nil {
		stream.OpenInputFile(0)
		stream.sendData()
	} else {
		if client.Files.FindInputByPath(path) != nil {
			client.NewLogStream(client.server, path)
		}
	}
	return nil
}

// HandleFileDelete shall close an existing stream
func (client *Client) HandleFileDelete(path string) error {
	stream := client.FindStreamByPath(path)
	if stream != nil {
		stream.Close()
	} else {
		log.Debug().Str("path", path).Msg("No stream found for path")
	}
	return nil
}

// NewLogStream stream
func NewLogStream(server, hostname, filename string) ClientLogStream {
	source := LogStream{nil, "", hostname, filename}
	s := ClientLogStream{&source, server, nil, 0, time.Now()}
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

// StreamFile will search for a stream in streams list
func (stream *ClientLogStream) StreamFile(path string, lastPos int64) (int64, error) {
	total := int64(0)
	var lastErr error
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

		err = stream.OpenInputFile(lastPos)
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("Unable to open file")
			return total, err
		}
		if stream.conn != nil {
			n, err := stream.streamFileData()
			total = total + n
			log.Debug().Err(err).Int64("read", n).Int64("pos", stream.LastPos).Msg("Stream data completed")
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
	stream.CloseInputFile()
	return total, lastErr
}

// sendData will read data and write to the steam connection
// TODO: this operation must be synced
func (stream *ClientLogStream) sendData() (int64, error) {
	if stream.InputFile == nil {
		log.Error().Msg("Input file is nil, return ErrClosedPipe")
		return 0, io.ErrClosedPipe
	}
	if stream.conn == nil {
		log.Error().Msg("Connection is nil, return ErrClosedPipe")
		return 0, io.ErrClosedPipe
	}
	total := int64(0)
	bufsize := int64(defaultBuffersize)
	// stream.InputFile.Seek(stream.LastPos, 0)
	for {
		log.Trace().Str("path", stream.filename).Int64("pos", stream.LastPos).Msg("Sending data from pos")
		n, err := io.CopyN(stream.conn, stream.InputFile, bufsize)
		if n > 0 && (err == nil || err == io.EOF) {
			log.Trace().Int64("n", n).Msg("Sent to stream")
			stream.LastPos = stream.LastPos + n
			stream.LastRead = time.Now()
			total = total + n
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Error().Err(err).Str("path", stream.filename).Msg("Error during stream data")
			stream.Close()
			// Strange workaround to restore correct position, to avoid sending too few data
			if (err == io.ErrClosedPipe || err == io.ErrUnexpectedEOF || strings.Contains(err.Error(), "broken pipe")) && total > int64(defaultBuffersize) {
				log.Warn().Msg("Apply workaround to avoid wrong position")
				total = total - int64(defaultBuffersize)
			}
			return total, err
		}
		if n == 0 {
			break
		}
	}
	if total > 0 {
		log.Debug().Str("stream", stream.streamID).Str("path", stream.filename).Int64("bytes", total).Msg("Sent data to stream")
	} else {
		log.Trace().Str("stream", stream.streamID).Str("path", stream.filename).Msg("No data sent to stream")
	}
	return total, nil
}

// StreamFile will search for a stream in streams list
func (stream *ClientLogStream) streamFileData() (total int64, err error) {
	log.Info().Str("path", stream.filename).Int64("pos", stream.LastPos).Msg("Stream file from position")
	for {
		n, err := stream.sendData()
		if err != nil {
			log.Error().Err(err).Str("path", stream.filename).Msg("Error during stream data")
			stream.Close()
			// Strange workaround to restore correct position, to avoid sending too few data
			if (err == io.ErrClosedPipe || err == io.ErrUnexpectedEOF || strings.Contains(err.Error(), "broken pipe")) && total > int64(defaultBuffersize) {
				log.Warn().Msg("Apply workaround to avoid wrong position")
				total = total - int64(defaultBuffersize)
			}
			break
		}
		if n > 0 {
			log.Debug().Str("stream", stream.streamID).Str("path", stream.filename).Int64("count", n).Int64("bytes", total).Msg("Sent data to stream")
			total = total + n
		} else {
			idle := time.Now().Sub(stream.LastRead)
			log.Debug().Str("path", stream.filename).Dur("idle", idle).Msg("Idle input file")
			if idle > 5*time.Second {
				time.Sleep(30 * time.Second)
			} else {
				time.Sleep(2 * time.Second)
			}

		}
	}
	log.Info().Str("stream", stream.streamID).Str("path", stream.filename).Int64("bytes", total).Msg("Sent data to stream")
	return total, nil
}

// OpenInputFile will open the inputfile for reading starting at
// the provided position
func (stream *ClientLogStream) OpenInputFile(pos int64) error {
	stream.CloseInputFile()
	log.Debug().Str("path", stream.filename).Msg("Opening input file")
	file, err := os.Open(stream.filename)
	if err != nil {
		log.Error().Err(err).Str("path", stream.filename).Msg("Failed to open input file")
		return err
	}
	stream.InputFile = file
	info, _ := stream.InputFile.Stat()
	log.Info().Str("path", stream.filename).Int64("size", info.Size()).Msg("Opened input file")
	if info.Size() < pos {
		log.Info().Int64("pos", stream.LastPos).Int64("size", info.Size()).Msg("Last position greater than file size. Starting from beginning")
		// TODO: Handle rotation gracefully
		stream.LastPos = 0
	}
	seekpos, err := stream.InputFile.Seek(pos, 0)
	if err != nil {
		log.Debug().Err(err).Str("path", stream.filename).Int64("pos", pos).Msg("Failed to seek to pos")
	}
	log.Debug().Str("path", stream.filename).Int64("pos", seekpos).Msg("Seeked to pos in input file")
	stream.LastPos = seekpos

	return nil
}

// CloseInputFile will close the inputfile for this stream
func (stream *ClientLogStream) CloseInputFile() {
	if stream.InputFile != nil {
		log.Info().Str("path", stream.filename).Msg("Closing input file")
		stream.InputFile.Close()
		stream.InputFile = nil
	}
}

// Close will close the stream
func (stream *ClientLogStream) Close() {
	stream.CloseInputFile()
	if stream.conn != nil {
		log.Info().Str("stream", stream.streamID).Msg("Closing stream")
		stream.conn.Close()
		stream.conn = nil
	} else {
		log.Debug().Str("stream", stream.streamID).Msg("Stream already closed")
	}
}
