package logster

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

// CommandPrefix used as delimiter for command detection
const CommandPrefix = "::"

// Client handles a logster client connection
type Client struct {
	server  string
	streams []*ClientLogStream
}

// ClientLogStream handles a log stream
type ClientLogStream struct {
	LogStream
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

	stream := ClientLogStream{LogStream{conn, "", hostname, file}, client}

	stream.writeCommand(fmt.Sprintf("INIT %s,%s", hostname, file))
	line, err := stream.awaitCommandResponse()
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
		if strings.Contains(err.Error(), "timeout") {
			log.Println("Timeout detected on stream", stream.streamID)
			stream.Close()
		}
		return nil, err
	}
	resp := strings.Split(line, " ")
	log.Println("[DBG] Response:", resp)
	stream.streamID = resp[1]

	s := append(client.streams, &stream)
	log.Println("[DBG] Adding new log stream:", stream)
	client.streams = s

	return &stream, nil
}

// CloseLogStream initiates a new log stream
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

// Reconnect the underlying connection
func (stream *ClientLogStream) Reconnect() error {
	log.Println("[DBG] Reconnecting stream", stream.streamID)
	stream.conn.Close()
	time.Sleep(2 * time.Second)
	conn, err := net.Dial("tcp", stream.client.server)
	if err != nil {
		log.Println("[ERROR] Unable to reconnect")
		return err
	}
	stream.conn = conn
	return nil
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
					err := stream.Reconnect()
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
	log.Println("Stream file from position", lastPos)
	stream.writeCommand("STREAM")
	resp, err := stream.awaitCommandResponse()
	if err != nil {
		log.Println("[ERROR] Stream command not accepted, no valid response", err)
		return 0, err
	}
	if !(strings.HasPrefix(resp, "OK") || strings.HasPrefix(resp, CommandPrefix+"OK")) {
		log.Println("[ERROR] Stream command not accepted '"+resp+"':", err)
		return 0, errors.New("Command stream not accepted: " + resp)
	}
	log.Println("Response", resp, "Seeking to position", lastPos)

	file.Seek(lastPos, 0)
	total := int64(0)
	bufsize := int64(512)
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

// writeCommand will write a single command to the server
func (stream LogStream) writeCommand(cmd string) error {
	conn := stream.conn
	writer := bufio.NewWriter(conn)
	// send to socket
	const timeoutDuration = 5 * time.Second
	conn.SetWriteDeadline(time.Now().Add(timeoutDuration))
	n, err := writer.WriteString(CommandPrefix + cmd + "\n")
	writer.Flush()
	log.Println("[DBG] Wrote command", cmd, "with", n, "bytes to stream", stream.streamID)
	return err
}

// awaitWriteCommand will write a single command to the server
func (stream ClientLogStream) awaitCommandResponse() (string, error) {
	conn := stream.conn
	reader := bufio.NewReader(conn)
	log.Println("[DBG] Reading and awaiting response on stream", stream.streamID)
	const timeoutDuration = 5 * time.Second
	conn.SetReadDeadline(time.Now().Add(timeoutDuration))
	line, err := reader.ReadString('\n')
	conn.SetReadDeadline(time.Unix(0, 0))
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
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

// Send will send a batch of stream data
func (stream ClientLogStream) Send(data string) error {
	conn := stream.conn
	// send to socket
	stream.writeCommand(fmt.Sprintf("SEND %d", len(data)))
	// write data to connection now
	writer := bufio.NewWriter(conn)
	len, err := writer.WriteString(data)
	writer.Flush()
	if err != nil {
		log.Println("Error during write:", err)
	}
	log.Println("[DBG] Wrote", len, "bytes to stream", stream.streamID, conn, "'", data, "'")
	line, err := stream.awaitCommandResponse()
	if err != nil {
		log.Println("[ERROR] Error during send:", err)
		return err
	}
	log.Println("[DBG] Response:", line)
	if strings.HasPrefix(line, CommandPrefix+"OK") {
		log.Println("[DBG] Send OK")
	}
	return nil
}

// Close will close the stream
func (stream ClientLogStream) Close() {
	stream.writeCommand(fmt.Sprintf("CLOSE %s", stream.streamID))
	line, err := stream.awaitCommandResponse()
	if err != nil {
		log.Println("[ERROR] Error during close:", err)
	} else {
		log.Println("[DBG] Response:", line)
		if strings.HasPrefix(line, CommandPrefix+"OK") {
			log.Println("[DBG] Send OK")
		}

	}
}
