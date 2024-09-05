package main

import (
	"bytes"
	"html/template"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v4"
)

type Message struct {
	ClientID string
	Count    int
}

type CandidateVote struct {
	Name    string `json:"Name"`
	VoteCnt int    `json:"VoteCnt"`
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

			for _, msg := range h.messages {
				client.send <- getMessageTemplate(msg)
			}
		case client := <-h.unregister:
			h.Lock()
			if _, ok := h.clients[client]; ok {
				log.Printf("client unregistered %s", client.id)
				close(client.send)
				delete(h.clients, client)
			}
			h.Unlock()
		case msg := <-h.broadcast:
			h.RLock()
			h.messages = append(h.messages, msg)
			for client := range h.clients {
				select {
				case client.send <- getMessageTemplate(msg):
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}

			h.RUnlock()
		}

	}
}

func (h *Hub) broadcastVoteCounts(db *pgx.Conn) {
	for {

		votes, err := getAllCandidates(db)
		if err != nil {
			log.Println("Error fetching votes:", err)
			continue
		}

		for _, nominee := range votes.Noms {
			h.broadcast <- &Message{ClientID: nominee.Name, Count: nominee.VoteCnt}
			log.Printf("Name: %v, Count: %v\n", nominee.Name, nominee.VoteCnt)
		}

		// time.Sleep(2 * time.Second) // Adjust interval as needed
		log.Println("Before sleep")
		time.Sleep(3 * time.Second)
		log.Println("After sleep")

	}
}

func getMessageTemplate(msg *Message) []byte {
	tmpl, parseErr := template.ParseFiles("div-count-template.html")
	if parseErr != nil {
		log.Fatalf("template parsing: %s", parseErr)
	}

	var renderedMessage bytes.Buffer
	executeErr := tmpl.Execute(&renderedMessage, msg)
	if executeErr != nil {
		log.Fatalf("template parsing: %s", executeErr)
	}

	return renderedMessage.Bytes()
}
