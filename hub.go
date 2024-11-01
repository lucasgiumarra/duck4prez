package main

import (
	"bytes"
	"html/template"
	"log"
	"sync"
)

type Message struct {
	ClientID string
	Data     []byte
}

type Hub struct {
	sync.RWMutex
	clients    map[*Client]bool
	broadcast  chan *Message
	update     chan *ClientVote
	register   chan *Client
	unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan *Message),
		update:     make(chan *ClientVote),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.Lock()
			h.clients[client] = true
			h.Unlock()
			log.Printf("client registered %s", client.id)
		case client := <-h.unregister:
			h.Lock()
			if _, ok := h.clients[client]; ok {
				log.Printf("client unregistered %s", client.id)
				delete(h.clients, client)
				close(client.send)
			}
			h.Unlock()
		case msg := <-h.broadcast:
			h.Lock()
			for client := range h.clients {
				select {
				case client.send <- getMessageTemplate(msg):
					log.Printf("Message sent to client %s", client.id)
				default:
					log.Printf("Failed to send message to client %s, unregistering", client.id)
					delete(h.clients, client)
					close(client.send)
				}
			}
			h.Unlock()
		case clientVote := <-h.update:
			h.Lock()
			dbConn, dbConnErr := connectDB()
			if dbConnErr != nil {
				log.Printf("Connection to database failed %s\n", dbConnErr)
			}
			incrementVoteErr := incrementVote(dbConn, clientVote.Name)
			if incrementVoteErr != nil {
				log.Printf("Error incrementing vote for %v: %v\n", clientVote.Name, incrementVoteErr)
			} else {
				log.Printf("Vote incremented for %v\n", clientVote.Name)
			}
			h.Unlock()
		}
	}
}

func getMessageTemplate(msg *Message) []byte {
	tmpl, parseErr := template.ParseFiles("/usr/local/bin/div-count-template.html")
	if parseErr != nil {
		log.Fatalf("template parsing error: %s", parseErr)
	}

	var renderedMessage bytes.Buffer
	executeErr := tmpl.Execute(&renderedMessage, msg)
	if executeErr != nil {
		log.Fatalf("template executing error: %s", executeErr)
	}

	return renderedMessage.Bytes()
}
