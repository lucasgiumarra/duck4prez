package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v4"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Nominee struct {
	Name    string
	VoteCnt int
}

type Nominees struct {
	Noms []Nominee
}

type Stats struct {
	Uptime       time.Time      `json:"uptime"`
	RequestCount uint64         `json:"requestCount"`
	Statuses     map[string]int `json:"statuses"`
	IPCounts     map[string]int `json:"ipCounts"`
	mutex        sync.RWMutex
}

func NewStats() *Stats {
	return &Stats{
		Uptime:   time.Now(),
		Statuses: map[string]int{},
		IPCounts: map[string]int{},
	}
}

func (s *Stats) ProcessStats(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := next(c); err != nil {
			c.Error(err)
		}
		s.mutex.Lock()
		defer s.mutex.Unlock()
		s.RequestCount++

		// Track status codes
		status := strconv.Itoa(c.Response().Status)
		s.Statuses[status]++

		// Track request by IP address
		ip := c.RealIP()
		s.IPCounts[ip]++

		return nil
	}
}

type TemplateRenderer struct {
	templates *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func connectDB() (*pgx.Conn, error) {
	conn, err := pgx.Connect(context.Background(), "postgresql://lugiumarra:BoatsandHoes11$@localhost:5432/nominees")
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func getVotes(db *pgx.Conn, candidateName string) (int, error) {
	var voteCount int
	err := db.QueryRow(context.Background(), "SELECT vote_count FROM candidates WHERE name=$1", candidateName).Scan(&voteCount)
	if err != nil {
		return 0, err
	}
	return voteCount, nil
}

func incrementVote(db *pgx.Conn, candidateName string) error {
	_, err := db.Exec(context.Background(), "UPDATE candidates SET vote_count = vote_count + 1 WHERE name=$1", candidateName)
	return err
}

func getAllCandidates(db *pgx.Conn) (Nominees, error) {
	rows, err := db.Query(context.Background(), "SELECT name, vote_count FROM candidates")
	if err != nil {
		return Nominees{}, err
	}
	defer rows.Close()

	var nominees Nominees
	for rows.Next() {
		var nom Nominee
		err := rows.Scan(&nom.Name, &nom.VoteCnt)
		if err != nil {
			return Nominees{}, err
		}
		nominees.Noms = append(nominees.Noms, nom)
	}

	if err := rows.Err(); err != nil {
		return Nominees{}, err
	}

	return nominees, nil
}

func (s *Stats) Handle(c echo.Context) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return c.JSON(http.StatusOK, s)
}

func main() {
	e := echo.New()
	hub := NewHub()

	// Enable debug mode
	e.Debug = true

	// Apply the rate limiter middleware globally
	// timeoutConfig := middleware.TimeoutWithConfig(middleware.TimeoutConfig{
	// 	Timeout: 5 * time.Minute,
	// })
	// rateLimiterConfig := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
	// 	Store: middleware.NewRateLimiterMemoryStoreWithConfig(
	// 		middleware.RateLimiterMemoryStoreConfig{Rate: 50, Burst: 200, ExpiresIn: 5 * time.Minute},
	// 	),
	// })
	s := NewStats()
	e.Use(s.ProcessStats)
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	db, err := connectDB()
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer db.Close(context.Background())

	go hub.Run()
	go hub.broadcastVoteCounts(db)

	// Serve static files from the /home/lugiumarra/htmx directory
	e.Static("/static", "/home/lugiumarra/htmx")
	e.Static("/images", "/home/lugiumarra/duck4prez/main/images")
	// e.Static("/", "home/lugiumarra/duck4prez/main")

	renderer := &TemplateRenderer{
		templates: template.Must(template.ParseGlob("index.html")),
	}
	e.Renderer = renderer

	e.GET("/", func(c echo.Context) error {
		nominees, getAllCandidatesErr := getAllCandidates(db)
		if getAllCandidatesErr != nil {
			return c.String(http.StatusInternalServerError, "Error fetching candidate votes")
		}

		return c.Render(http.StatusOK, "index.html", nominees)
	})

	e.GET("/stats", s.Handle)
	e.GET("/ws", func(c echo.Context) error {
		ServeWs(hub, c.Response(), c.Request())
		return nil
	}) // WebSocket endpoint

	e.GET("/vote/:candidate", func(c echo.Context) error {
		candidate := c.Param("candidate")
		incErr := incrementVote(db, candidate)
		if incErr != nil {
			return c.String(http.StatusInternalServerError, "Error updating vote count")
		}
		votes, votesErr := getVotes(db, candidate)
		if votesErr != nil {
			return c.String(http.StatusInternalServerError, "Error fetching votes")
		}
		return c.String(http.StatusOK, fmt.Sprintf("%d", votes))
	})

	e.Logger.Fatal(e.Start(":42069"))
}
