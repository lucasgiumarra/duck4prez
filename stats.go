package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

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

func (s *Stats) Handle(c echo.Context) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return c.JSON(http.StatusOK, s)
}
