package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func Dial(socketPath string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	return &Client{conn: conn, scanner: scanner}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Send(request protocol.Request) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.conn.Write(data)
	return err
}

func (c *Client) ReadEvent() (protocol.Event, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return protocol.Event{}, err
		}
		return protocol.Event{}, net.ErrClosed
	}
	var event protocol.Event
	if err := json.Unmarshal(c.scanner.Bytes(), &event); err != nil {
		return protocol.Event{}, err
	}
	return event, nil
}
