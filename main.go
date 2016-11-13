package girc

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net"
	"time"
)

// TODO: See all todos!

// Client contains all of the information necessary to run a single IRC client
type Client struct {
	Config Config      // configuration for client
	State  *State      // state for the client
	Events chan *Event // queue of events to handle
	Sender Sender      // send wrapper for conn

	initTime  time.Time             // time when the client was created
	callbacks map[string][]Callback // mapping of callbacks
	reader    *Decoder              // for use with reading from conn stream
	writer    *Encoder              // for use with writing to conn stream
	conn      net.Conn              // network connection to the irc server
	tries     int                   // number of attempts to connect to the server
	log       *log.Logger           // package logger
	quitChan  chan bool             // channel used for disconnect/quitting
}

// Config contains configuration options for an IRC client
type Config struct {
	Server         string      // server to connect to
	Port           int         // port to use for server
	Password       string      // password for the irc server
	Nick           string      // nickname to attempt to use on connect
	User           string      // username to attempt to use on connect
	Name           string      // "realname" to attempt to use on connect
	TLSConfig      *tls.Config // tls/ssl configuration
	MaxRetries     int         // max number of reconnect retries
	Logger         io.Writer   // writer for which to write logs to
	DisableHelpers bool        // if default event handlers should be used (to respond to ping, user tracking, etc)
}

// New creates a new IRC client with the specified server, name and config
func New(config Config) *Client {
	client := &Client{
		Config:    config,
		Events:    make(chan *Event, 10), // buffer 10 events
		quitChan:  make(chan bool),
		callbacks: make(map[string][]Callback),
		tries:     0,
		initTime:  time.Now(),
	}

	// register builtin helpers
	if !client.Config.DisableHelpers {
		client.registerHelpers()
	}

	return client
}

// Quit disconnects from the server
func (c *Client) Quit() {
	// TODO: sent QUIT?
	if c.conn != nil {
		c.conn.Close()
	}

	c.quitChan <- true
}

// Uptime returns the amount of time that has passed since the client was created
func (c *Client) Uptime() time.Duration {
	return time.Now().Sub(c.initTime)
}

// Server returns the string representation of host+port pair for net.Conn
func (c *Client) Server() string {
	return fmt.Sprintf("%s:%d", c.Config.Server, c.Config.Port)
}

// Send is a handy wrapper around Sender
func (c *Client) Send(event *Event) error {
	// log the event
	if !event.Sensitive {
		c.log.Print("[write] ", event.String())
	}

	return c.Sender.Send(event)
}

// Connect attempts to connect to the given IRC server
func (c *Client) Connect() error {
	var conn net.Conn
	var err error

	// sanity check a few things here...
	if c.Config.Server == "" || c.Config.Port == 0 || c.Config.Nick == "" || c.Config.User == "" {
		return errors.New("invalid configuration (server/port/nick/user)")
	}

	// reset our state here
	c.State = NewState()

	if c.Config.Logger == nil {
		c.Config.Logger = ioutil.Discard
	}

	c.log = log.New(c.Config.Logger, "", log.Ldate|log.Ltime|log.Lshortfile)

	if c.Config.TLSConfig == nil {
		conn, err = net.Dial("tcp", c.Server())
	} else {
		conn, err = tls.Dial("tcp", c.Server(), c.Config.TLSConfig)
	}
	if err != nil {
		return err
	}

	c.conn = conn
	c.reader = NewDecoder(conn)
	c.writer = NewEncoder(conn)
	c.Sender = serverSender{writer: c.writer}
	for _, event := range c.connectMessages() {
		if err := c.Send(event); err != nil {
			return err
		}
	}

	c.tries = 0
	go c.ReadLoop()

	// consider the connection a success at this point
	c.State.connected = true

	return nil
}

// connectMessages is a list of IRC messages to send when attempting to
// connect to the IRC server.
func (c *Client) connectMessages() []*Event {
	events := []*Event{}

	// passwords first
	if c.Config.Password != "" {
		events = append(events, &Event{Command: PASS, Params: []string{c.Config.Password}})
	}

	// send nickname
	events = append(events, &Event{Command: NICK, Params: []string{c.Config.Nick}})

	// then username and realname
	if c.Config.Name == "" {
		c.Config.Name = c.Config.User
	}

	events = append(events, &Event{
		Command:  USER,
		Params:   []string{c.Config.User, "+iw", "*"},
		Trailing: c.Config.Name,
	})

	return events
}

// Reconnect checks to make sure we want to, and then attempts to
// reconnect to the server
func (c *Client) Reconnect() error {
	if c.Config.MaxRetries > 0 {
		c.conn.Close()
		var err error

		// sleep for 10 seconds so we're not slaughtering the server
		c.log.Printf("reconnecting to %s in 10 seconds", c.Server())
		time.Sleep(10 * time.Second)

		for err = c.Connect(); err != nil && c.tries < c.Config.MaxRetries; c.tries++ {
			duration := time.Duration(math.Pow(2.0, float64(c.tries))*200) * time.Millisecond
			time.Sleep(duration)
		}

		return err
	}

	close(c.Events)
	return nil
}

// ReadLoop sets a timeout of 300 seconds, and then attempts to read
// from the IRC server. If there is an error, it calls Reconnect
func (c *Client) ReadLoop() error {
	for {
		c.conn.SetDeadline(time.Now().Add(300 * time.Second))
		event, err := c.reader.Decode()
		if err != nil {
			return c.Reconnect()
		}

		// TODO: not adding PRIVMSG entries?
		c.Events <- event
	}
}

// Wait reads from the events channel and sends the events to be handled
// for every message it recieves.
func (c *Client) Wait() {
	var e *Event
	for {
		select {
		case e = <-c.Events:
			c.handleEvent(e)
		case <-c.quitChan:
			return
		}
	}
}

// IsConnected returns true if the client is connected to the server
func (c *Client) IsConnected() bool {
	c.State.m.RLock()
	defer c.State.m.RUnlock()

	return c.State.connected
}

// GetNick returns the current nickname of the active connection
func (c *Client) GetNick() string {
	c.State.m.RLock()
	defer c.State.m.RUnlock()

	if c.State.nick == "" {
		return c.Config.Nick
	}

	return c.State.nick
}

// SetNick changes the client nickname
func (c *Client) SetNick(name string) {
	c.State.m.Lock()
	defer c.State.m.Unlock()

	c.State.nick = name
	c.Send(&Event{Command: NICK, Params: []string{name}})
}

func (c *Client) GetChannels() map[string]*Channel {
	c.State.m.RLock()
	defer c.State.m.RUnlock()

	return c.State.channels
}

// Who tells the client to update it's channel/user records
func (c *Client) Who(channel string) {
	c.Send(&Event{Command: WHO, Params: []string{channel, "%tcuhn,1"}})
}