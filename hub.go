package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"
	"sync"

	"github.com/jackc/pgx/v4"
)

type Message struct {
	ClientID string
	Data     string
}

type Hub struct {
	sync.RWMutex
	clients    map[*Client]bool
	messages   []*Message
	broadcast  chan *Message
	register   chan *Client
	unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan *Message),
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
			h.messages = append(h.messages, msg)
			for client := range h.clients {
				select {
				case client.send <- getMessageTemplate(msg):
				default:
					log.Printf("Failed to send message to client %s, unregistering", client.id)
					delete(h.clients, client)
					close(client.send)
				}
			}
			h.Unlock()
		}
	}
}

func (h *Hub) broadcastVoteCounts(db *pgx.Conn) error {
	// Fetch all the votes at once
	votes, getVotesErr := getAllCandidates(db)
	if getVotesErr != nil {
		log.Println("Error fetching votes:", getVotesErr)
		return getVotesErr
	}

	// Marshal the vote counts into a single JSON payload
	payload, marshErr := json.Marshal(votes.Noms)
	if marshErr != nil {
		log.Println("Error marshalling votes:", marshErr)
		return marshErr
	}

	// Broadcast the payload to all connected clients
	h.broadcast <- &Message{
		ClientID: "all", // You can set a general ID if necessary
		Data:     string(payload),
	}

	// log.Println("Broadcasting vote counts to all clients")
	log.Printf("Broadcasting payload: %s\n", string(payload))
	return nil
}

// func (h *Hub) broadcastVoteCounts(db *pgx.Conn) error {
// 	// Fetch the vote counts only when needed
// 	votes, err := getAllCandidates(db)
// 	if err != nil {
// 		log.Println("Error fetching votes:", err)
// 		return err
// 	}
//
// 	for _, nominee := range votes.Noms {
// 		// Send the updated vote counts to the connected clients
// 		h.broadcast <- &Message{ClientID: nominee.Name, Data: nominee.VoteCnt}
// 		log.Printf("Name: %v, Count: %v\n", nominee.Name, nominee.VoteCnt)
// 	}
//
// 	return nil
// }

func getMessageTemplate(msg *Message) []byte {
	tmpl, parseErr := template.ParseFiles("div-count-template.html")
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
