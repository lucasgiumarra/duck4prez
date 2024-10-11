package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"

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

type TemplateRenderer struct {
	templates *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func connectDB() (*pgx.Conn, error) {
	conn, err := pgx.Connect(context.Background(), "postgres://postgres:rPocFaOp5VP3kP8@d4p-db.flycast:5432")
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

func main() {
	e := echo.New()
	hub := NewHub()
	go hub.Run()

	// Enable debug mode
	e.Debug = true

	s := NewStats()
	e.Use(s.ProcessStats)
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	db, err := connectDB()
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer db.Close(context.Background())

	// Serve static files from the /home/lugiumarra/htmx directory
	e.Static("/static", "/home/lugiumarra/htmx")
	e.Static("/images", "/home/lugiumarra/duck4prez/main/images")
	// Serve static CSS files
	e.Static("/css", "/home/lugiumarra/duck4prez/main/css")
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

	e.GET("/getVotes", func(c echo.Context) error {
		// Retrive all candidates' votes from database
		votes, getVotesErr := getAllCandidates(db)
		if getVotesErr != nil {
			return c.String(http.StatusInternalServerError, "Error fetching votes")
		}

		return c.JSON(http.StatusOK, votes) // Return all votes as a JSON object
		// candidate := c.Param("candidate")
		// votes, votesErr := getVotes(db, candidate)
		// if votesErr != nil {
		// 	return c.String(http.StatusInternalServerError, "Error fetching votes")
		// }
		// return c.String(http.StatusOK, fmt.Sprintf("%d", votes))
	})

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

	e.GET("/get/:candidate", func(c echo.Context) error {
		candidate := c.Param("candidate")
		voteCount, errVoteCount := getVotes(db, candidate)
		if errVoteCount != nil {
			return c.String(http.StatusInternalServerError, "Error fetching vote count")
		}
		return c.String(http.StatusOK, fmt.Sprintf("%d", voteCount))
	})

	e.Logger.Fatal(e.Start(":8080"))
}
