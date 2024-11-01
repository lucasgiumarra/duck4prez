package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v4"
)

const (
	writeWait             = 10 * time.Second
	pingPeriod            = (pongWait * 9) / 10
	pongWait              = 2 * time.Minute // Set inactivity timeout to 2 minutes
	maxMessageSize        = 512
	limitRequestPerMinute = 1000
)

type ClientVote struct {
	Name string `json:"Name"`
}

type Client struct {
	id         string
	hub        *Hub
	conn       *websocket.Conn
	dbConn     *pgx.Conn
	send       chan []byte
	lastActive time.Time // Track last activity time for rate limiting
	request    int       // Track number of request
}

type RateLimiter struct {
	request   int
	lastCheck time.Time
	limit     int
	interval  time.Duration
}

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error while upgrading connection", err)
		return
	}
	// Ensure connection is closed when the function exits
	defer conn.Close()

	id := uuid.New().String()

	dbConn, dbConnErr := connectDB()
	if dbConnErr != nil {
		log.Printf("ServeWs database connection error: %v\n", dbConnErr)
		return // Early return if database connection fails
	}
	// Ensure database connection is closed when the function exits, if applicable
	// defer func() {
	// 	dbCloseErr := dbConn.Close(context.Background())
	// 	if dbCloseErr != nil {
	// 		log.Printf("Error closing database connection: %v\n", dbCloseErr)
	// 	}
	// }()

	client := &Client{
		id:         id,
		hub:        hub,
		conn:       conn,
		dbConn:     dbConn,
		send:       make(chan []byte),
		lastActive: time.Now(),
		request:    0,
	}
	log.Printf("Client registered: %s\n", client.id)
	client.hub.register <- client
	go client.writePump()
	go client.readPump()
}

// Reading messages from a client
func (c *Client) readPump() {
	defer func() {
		c.conn.Close()
		c.dbConn.Close(context.Background())
		c.hub.unregister <- c
		log.Println("Closing connections")
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(appData string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, text, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("unexpected close error for client %s: %v", c.id, err)
			} else {
				log.Printf("read error for client %s: %v", c.id, err)
			}
			break
		}

		// Rate limiting
		if !c.AllowRequest() {
			log.Printf("Rate limit exceeded for client: %s, current request count: %d", c.id, c.request)
			c.conn.WriteMessage(websocket.TextMessage, []byte("Rate limit exceeded"))
			continue
		}

		log.Printf("Server received message from client%s: %s", c.id, text)

		var clientVote *ClientVote
		if string(text) == "ping" {
			continue
		} else {
			// Unmarshal parses json and puts it into clientVote
			marshErr := json.Unmarshal(text, &clientVote)
			if marshErr != nil {
				log.Printf("read pump for client %s: JSON unmarshaling error: %v\n", c.id, marshErr)
				continue // Skip to the next iteration
			}
			c.hub.update <- clientVote
		}

		log.Printf("Client Vote: %v\n", clientVote.Name)

		votes, votesErr := getVotes(c.dbConn, clientVote.Name)
		if votesErr != nil {
			log.Printf("readPump: getVotes err: %v\n", votesErr)
		} else {
			log.Printf("Votes for %s: %v\n", clientVote.Name, votes)
		}

		var votesByte []byte
		votesByte = append(votesByte, byte(votes))
		votesByte = append(votesByte, text...)
		message := &Message{
			ClientID: c.id, // The client who sent this message
			Data:     votesByte,
		}
		log.Printf("Broadcasting message for client %s: %v\n", c.id, message)

		// Send the broadcast message to all connected clients
		c.hub.broadcast <- message

		// Update last active time on each message
		c.lastActive = time.Now()
	}
}

// Writes messages from server to client
// Sends periodic messages to keep connection alive
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				writeMessageErr := c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				if writeMessageErr != nil {
					log.Printf("Error sending close message: %v", writeMessageErr)
				}
				return
			}

			w, nextWriterErr := c.conn.NextWriter(websocket.TextMessage)
			if nextWriterErr != nil {
				log.Printf("Error getting next writer: %v", nextWriterErr)
				return
			}

			_, writeErr := w.Write(msg)
			if writeErr != nil {
				log.Printf("Error writing message: %v", writeErr)
			}

			n := len(c.send)
			for i := 0; i < n; i++ {
				_, writeBuffErr := w.Write(msg)
				if writeBuffErr != nil {
					log.Printf("Error writing buffered message: %v", writeBuffErr)
				}
			}

			closeErr := w.Close()
			if closeErr != nil {
				log.Printf("Error closing writer: %v", closeErr)
				return
			}
			log.Printf("Server sent message to client: %s", msg)

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			// log.Println("Sending Ping")
			pingErr := c.conn.WriteMessage(websocket.PingMessage, nil)
			if pingErr != nil {
				log.Printf("Error sending ping message: %v", pingErr)
				return
			}
		}
	}
}

func (c *Client) AllowRequest() bool {
	now := time.Now()

	// Reset request count if a minute has passed
	if now.Sub(c.lastActive) > time.Minute {
		c.request = 0
		c.lastActive = now
	}

	// Allow if under request limit
	if c.request < limitRequestPerMinute {
		c.request++
		return true
	}

	return false
}
