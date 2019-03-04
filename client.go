package logster

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
)

// CommandPrefix used as delimiter for command detection
const CommandPrefix = "::"

// Client handles a logster client connection
type Client struct {
	conn *net.Conn
}

// ClientLogStream handles a log stream
type ClientLogStream struct {
	client   Client
	streamID string
}

// NewClient initiates a new client connection
func NewClient(server string) (*Client, error) {
	// connect to this socket
	conn, err := net.Dial("tcp", server)
	if err != nil {
		log.Println("Failed to connect to server", server)
		return nil, err
	}

	client := Client{&conn}
	return &client, err
}

// writeCommand will write a single command to the server
func (client Client) writeCommand(cmd string) error {
	conn := *client.conn
	writer := bufio.NewWriter(conn)
	// send to socket
	int, err := writer.WriteString(cmd)
	log.Println("Wrote", int, "bytes to server")
	return err
}

// awaitWriteCommand will write a single command to the server
func (client Client) awaitCommandResponse() (string, error) {
	conn := *client.conn
	reader := bufio.NewReader(conn)
	line, _, err := reader.ReadLine()
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
	}
	return string(line), err
}

// NewLogStream initiates a new log stream
func (client Client) NewLogStream(hostname string, file string) (ClientLogStream, error) {
	client.writeCommand(fmt.Sprintf("%sINIT %s,%s", CommandPrefix, hostname, file))
	line, err := client.awaitCommandResponse()
	streamID := ""
	if err != nil {
		log.Println("ERROR on awaitResponse:", err)
	} else {
		resp := strings.Split(line, " ")
		streamID = resp[1]
	}

	stream := ClientLogStream{client, streamID}
	return stream, err
}

func (stream ClientLogStream) send(data string) {
	conn := *stream.client.conn
	// send to socket
	stream.client.writeCommand(fmt.Sprintf("%sSEND %s,%d", CommandPrefix, stream.streamID, len(data)))
	// write data to connection now
	writer := bufio.NewWriter(conn)
	len, err := writer.WriteString(data)
	if err != nil {
		log.Println("Error during write:", err)
	}
	log.Println("Wrote", len, "bytes to stream")
	line, err := stream.client.awaitCommandResponse()
	if err != nil {
		log.Println("Error during send:", err)
	} else {
		log.Println("Response:", line)
		if strings.HasPrefix(line, CommandPrefix+"OK") {
			log.Println("Send OK")
		}

	}
}

func (stream ClientLogStream) close() {
	stream.client.writeCommand(fmt.Sprintf("%sCLOSE %s", CommandPrefix, stream.streamID))
	line, err := stream.client.awaitCommandResponse()
	if err != nil {
		log.Println("Error during close:", err)
	} else {
		log.Println("Response:", line)
		if strings.HasPrefix(line, CommandPrefix+"OK") {
			log.Println("Send OK")
		}

	}
}
