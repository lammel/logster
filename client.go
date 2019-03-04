package logster

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"
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
		log.Println("Failed to connect to server", client.server)
		return nil, err
	}

	source := LogStream{conn, "", hostname, file}
	stream := ClientLogStream{&source, client}

	line, err := stream.awaitMessage()
	if err != nil {
		log.Println("Failed to await Hello from server. Aborting")
		stream.Close()
		return nil, err
	}
	log.Println("Connected to server:", line)
	line, err = stream.awaitMessage()
	if err != nil {
		log.Println("Failed to await StreamID from server. Aborting")
		stream.Close()
		return nil, err
	}
	resp := strings.Split(line, " ")
	log.Println("[DBG] Response:", resp)
	if resp[0] != "STREAMID" {
		log.Println("Could not identify stream ID, aborting")
		stream.Close()
		return nil, err
	}
	stream.streamID = resp[1]
	log.Println("[DBG] Using streamID:", stream.streamID)

	stream.writeMessage(fmt.Sprintf("INIT STREAM %s:%s", hostname, file))
	line, err = stream.awaitMessage()
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
		if strings.Contains(err.Error(), "timeout") {
			log.Println("Timeout detected on stream", stream.streamID)
			stream.Close()
		}
		return nil, err
	}
	log.Println("[DBG] Response:", line)
	if !strings.HasPrefix(line, "OK") {
		log.Println("Failed to init stream")
		stream.Close()
	}

	s := append(client.streams, &stream)
	log.Println("[DBG] Adding new log stream:", stream)
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
			log.Println("Error during send")
			if strings.Contains(err.Error(), "timeout") {
				log.Println("Timeout on stream detected, recreating stream")
			}
			client.CloseLogStream(stream)
			newstream, _ := client.NewLogStream(stream.hostname, stream.filename)
			err = newstream.Send(data)

		}
	} else {
		log.Println("Unable to find stream for path", path)
	}
	return nil
}

// FindStreamByPath will search for a stream in streams list
func (client *Client) FindStreamByPath(path string) *ClientLogStream {
	var stream *ClientLogStream
	streams := client.streams
	for _, s := range streams {
		if s == nil {
			log.Println("Skipping nil stream", s)
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
			log.Println("Unable to open file", path, ":", err)
			return total, err
		}
		info, _ := file.Stat()
		if info.Size() < pos {
			log.Println("Last position greater than file size", pos, ">", info.Size(), "Starting from beginning of file")
			pos = 0
		}
		time.Sleep(1 * time.Second)
		n, err := stream.StreamFile(file, pos)
		total = total + n
		pos = pos + n
		log.Println("File", stream.filename, "new pos:", pos, "read:", n)

		if err != nil {
			retry = retry + 1
			if strings.Contains(err.Error(), " not accepted") {
				if retry > 3 {
					err := stream.Reconnect(stream.client.server)
					if err != nil {
						log.Println("Error during reconnect")
					}
				}
			} else {
				log.Println("[ERROR] StreamFile returned error", err)
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
	log.Println("Stream file ", file.Name(), "from position", lastPos)
	file.Seek(lastPos, 0)
	total := int64(0)
	bufsize := int64(defaultBuffersize)
	for {
		n, err := io.CopyN(stream.conn, file, bufsize)
		total = total + n
		if n > 0 {
			log.Println("Sent", n, "bytes to stream", stream.streamID, "from file", file.Name())
		}
		if err != nil {
			if err == io.EOF {
				log.Println("End Of File (EOF) reached for", file.Name())
				time.Sleep(1 * time.Second)
				continue
			}
			log.Println("Error during StreamFile for", file.Name(), err)
			stream.conn.Close()
			return total, err
		}
		log.Println("Read and written", n, "bytes.")
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
		log.Println("Error during write:", err)
	}
	log.Println("[DBG] Wrote", len, "bytes to stream", stream.streamID, conn, "'", data, "'")
	line, err := stream.awaitMessage()
	if err != nil {
		log.Println("[ERROR] Error during send:", err)
		return err
	}
	log.Println("[DBG] Response:", line)
	if strings.HasPrefix(line, "OK") {
		log.Println("[DBG] Send OK")
	}
	return nil
}

// Close will close the stream
func (stream ClientLogStream) Close() {
	stream.writeMessage(fmt.Sprintf("CLOSE %s", stream.streamID))
	line, err := stream.awaitMessage()
	if err != nil {
		log.Println("[ERROR] Error during close:", err)
	} else {
		log.Println("[DBG] Response:", line)
		if strings.HasPrefix(line, "OK") {
			log.Println("[DBG] Send OK")
		}

	}
}
