package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"ticket-reservation/cluster"
	"ticket-reservation/db"
	"ticket-reservation/models"
	"ticket-reservation/service"
)

// Server represents the HTTP API server
type Server struct {
	client       *cluster.Client
	svc          *service.ReservationService
	readThrough  *service.ReadThroughCache
	postgres     *db.PostgresDB
	addr         string
}

// NewServer creates a new API server with optional PostgreSQL integration
func NewServer(addr string, reservationTTL time.Duration, pgDSN string) (*Server, error) {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis cluster: %w", err)
	}

	var pg *db.PostgresDB
	if pgDSN != "" {
		pg, err = db.NewPostgresDB(pgDSN)
		if err != nil {
			log.Printf("[PostgreSQL] Connection failed: %v (running in Redis-only mode)", err)
			pg = nil
		}
	}

	var svc *service.ReservationService
	if pg != nil {
		svc = service.NewReservationServiceWithPG(client.Redis(), pg, reservationTTL)
		log.Println("[Server] Running with PostgreSQL integration")
	} else {
		svc = service.NewReservationService(client.Redis(), reservationTTL)
		log.Println("[Server] Running in Redis-only mode")
	}

	var rtCache *service.ReadThroughCache
	if pg != nil {
		rtCache = service.NewReadThroughCache(client.Redis(), pg)
	}

	return &Server{
		client:      client,
		svc:         svc,
		readThrough: rtCache,
		postgres:    pg,
		addr:        addr,
	}, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", s.handleHealth)

	// Cluster info
	mux.HandleFunc("/cluster/info", s.handleClusterInfo)

	// Event endpoints
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/events/", s.handleEventByID)

	// Reservation endpoints
	mux.HandleFunc("/reservations", s.handleReservations)
	mux.HandleFunc("/reservations/", s.handleReservationByID)

	// Waitlist endpoint
	mux.HandleFunc("/waitlist", s.handleWaitlist)

	// Reconciliation endpoint (Part 7)
	mux.HandleFunc("/reconcile", s.handleReconcile)

	log.Printf("Starting API server on %s", s.addr)
	if s.postgres != nil {
		log.Println("PostgreSQL integration: ENABLED")
	} else {
		log.Println("PostgreSQL integration: DISABLED (set PG_DSN to enable)")
	}
	return http.ListenAndServe(s.addr, s.logMiddleware(mux))
}

// Close closes the server connections
func (s *Server) Close() error {
	if s.postgres != nil {
		s.postgres.Close()
	}
	return s.client.Close()
}

// Middleware for logging
func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// Response helpers
func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// Health check handler
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	err := s.client.Ping()
	if err != nil {
		errorResponse(w, http.StatusServiceUnavailable, "redis cluster unavailable")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Cluster info handler
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	info, err := s.client.GetClusterInfo()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, info)
}

// Events handler (list/create)
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createEvent(w, r)
	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// CreateEventRequest represents the request body for creating an event
type CreateEventRequest struct {
	Name         string  `json:"name"`
	Venue        string  `json:"venue"`
	Date         string  `json:"date"` // RFC3339 format
	Rows         int     `json:"rows"`
	SeatsPerRow  int     `json:"seats_per_row"`
	PricePerSeat float64 `json:"price_per_seat"`
}

func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	var req CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		errorResponse(w, http.StatusBadRequest, "name is required")
		return
	}

	// Set defaults
	if req.Venue == "" {
		req.Venue = "Main Hall"
	}
	if req.Rows == 0 {
		req.Rows = 10
	}
	if req.SeatsPerRow == 0 {
		req.SeatsPerRow = 10
	}
	if req.PricePerSeat == 0 {
		req.PricePerSeat = 50.00
	}

	eventDate := time.Now().Add(30 * 24 * time.Hour)
	if req.Date != "" {
		parsed, err := time.Parse(time.RFC3339, req.Date)
		if err == nil {
			eventDate = parsed
		}
	}

	pattern := r.URL.Query().Get("pattern")

	switch pattern {
	case "write-around":
		// Write-Around: write only to PostgreSQL, skip Redis
		event := &models.Event{
			ID:           fmt.Sprintf("wa-%d", time.Now().UnixNano()%100000),
			Name:         req.Name,
			Venue:        req.Venue,
			Date:         eventDate,
			TotalSeats:   req.Rows * req.SeatsPerRow,
			Rows:         req.Rows,
			SeatsPerRow:  req.SeatsPerRow,
			PricePerSeat: req.PricePerSeat,
			CreatedAt:    time.Now(),
		}
		if err := s.svc.CreateEventWriteAround(event); err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]interface{}{
			"pattern": "write-around",
			"event":   event,
		})
	default:
		// Default: Write-Through (write to both PG and Redis)
		event, err := s.svc.CreateEvent(req.Name, req.Venue, eventDate, req.Rows, req.SeatsPerRow, req.PricePerSeat)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, event)
	}
}

// Event by ID handler
func (s *Server) handleEventByID(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimPrefix(r.URL.Path, "/events/")
	if eventID == "" {
		errorResponse(w, http.StatusBadRequest, "event ID required")
		return
	}

	// Check for sub-resources
	parts := strings.Split(eventID, "/")
	eventID = parts[0]

	if len(parts) > 1 {
		switch parts[1] {
		case "availability":
			s.getAvailability(w, r, eventID)
		case "seats":
			s.getSeats(w, r, eventID)
		default:
			errorResponse(w, http.StatusNotFound, "not found")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getEvent(w, r, eventID)
	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request, eventID string) {
	pattern := r.URL.Query().Get("pattern")

	var event interface{}
	var err error

	switch pattern {
	case "cache-aside":
		event, err = s.svc.GetEventCacheAside(eventID)
	case "read-through":
		if s.readThrough == nil {
			errorResponse(w, http.StatusServiceUnavailable, "Read-Through requires PostgreSQL (set PG_DSN)")
			return
		}
		event, err = s.readThrough.GetEvent(eventID)
	case "refresh-ahead":
		event, err = s.svc.GetEventRefreshAhead(eventID)
	default:
		// Default: original GetEvent (fallback pattern)
		event, err = s.svc.GetEvent(eventID)
	}

	if err != nil {
		errorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, event)
}

func (s *Server) getAvailability(w http.ResponseWriter, r *http.Request, eventID string) {
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	pattern := r.URL.Query().Get("pattern")

	var stats interface{}
	var err error

	switch pattern {
	case "read-through":
		if s.readThrough == nil {
			errorResponse(w, http.StatusServiceUnavailable, "Read-Through requires PostgreSQL (set PG_DSN)")
			return
		}
		stats, err = s.readThrough.GetEventStats(eventID)
	default:
		// Default: original GetAvailability (with fallback)
		stats, err = s.svc.GetAvailability(eventID)
	}

	if err != nil {
		errorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, stats)
}

func (s *Server) getSeats(w http.ResponseWriter, r *http.Request, eventID string) {
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	pattern := r.URL.Query().Get("pattern")

	switch pattern {
	case "cache-aside":
		// Cache-Aside: returns all seats with status (not just available)
		seatsMap, err := s.svc.GetSeatsCacheAside(eventID)
		if err != nil {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"event_id": eventID,
			"pattern":  "cache-aside",
			"seats":    seatsMap,
			"count":    len(seatsMap),
		})
	default:
		// Default: original GetAvailableSeats
		seats, err := s.svc.GetAvailableSeats(eventID)
		if err != nil {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"event_id":        eventID,
			"available_seats": seats,
			"count":           len(seats),
		})
	}
}

// Reservations handler
func (s *Server) handleReservations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createReservation(w, r)
	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ReserveRequest represents the request body for creating a reservation
type ReserveRequest struct {
	EventID       string   `json:"event_id"`
	UserID        string   `json:"user_id"`
	Seats         []string `json:"seats"`
	CustomerName  string   `json:"customer_name"`
	CustomerEmail string   `json:"customer_email"`
}

func (s *Server) createReservation(w http.ResponseWriter, r *http.Request) {
	var req ReserveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.EventID == "" || req.UserID == "" || len(req.Seats) == 0 {
		errorResponse(w, http.StatusBadRequest, "event_id, user_id, and seats are required")
		return
	}

	// Normalize seat IDs to uppercase
	for i, seat := range req.Seats {
		req.Seats[i] = strings.ToUpper(strings.TrimSpace(seat))
	}

	reservation, err := s.svc.ReserveSeats(req.EventID, req.UserID, req.Seats, req.CustomerName, req.CustomerEmail)
	if err != nil {
		// Check if it's a seat unavailable error
		if strings.Contains(err.Error(), "not available") {
			errorResponse(w, http.StatusConflict, err.Error())
			return
		}
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, reservation)
}

// Reservation by ID handler
func (s *Server) handleReservationByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/reservations/")
	parts := strings.Split(path, "/")
	reservationID := parts[0]

	if reservationID == "" {
		errorResponse(w, http.StatusBadRequest, "reservation ID required")
		return
	}

	// Check for actions
	if len(parts) > 1 {
		switch parts[1] {
		case "confirm":
			s.confirmReservation(w, r, reservationID)
		case "cancel":
			s.cancelReservation(w, r, reservationID)
		default:
			errorResponse(w, http.StatusNotFound, "not found")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getReservation(w, r, reservationID)
	case http.MethodDelete:
		s.cancelReservation(w, r, reservationID)
	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getReservation(w http.ResponseWriter, r *http.Request, reservationID string) {
	reservation, err := s.svc.GetReservation(reservationID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, reservation)
}

// ConfirmRequest represents the request body for confirming a reservation
type ConfirmRequest struct {
	PaymentID string `json:"payment_id"`
}

func (s *Server) confirmReservation(w http.ResponseWriter, r *http.Request, reservationID string) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ConfirmRequest
	json.NewDecoder(r.Body).Decode(&req) // Optional body

	if req.PaymentID == "" {
		req.PaymentID = fmt.Sprintf("pay_%d", time.Now().UnixNano())
	}

	reservation, err := s.svc.ConfirmReservation(reservationID, req.PaymentID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "expired") {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, reservation)
}

func (s *Server) cancelReservation(w http.ResponseWriter, r *http.Request, reservationID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	err := s.svc.CancelReservation(reservationID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{
		"status":  "cancelled",
		"message": "Reservation cancelled and seats released",
	})
}

// Waitlist handler
func (s *Server) handleWaitlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		EventID        string `json:"event_id"`
		UserID         string `json:"user_id"`
		Email          string `json:"email"`
		RequestedSeats int    `json:"requested_seats"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.EventID == "" || req.UserID == "" || req.Email == "" {
		errorResponse(w, http.StatusBadRequest, "event_id, user_id, and email are required")
		return
	}

	if req.RequestedSeats == 0 {
		req.RequestedSeats = 1
	}

	entry, err := s.svc.JoinWaitlist(req.EventID, req.UserID, req.Email, req.RequestedSeats)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, entry)
}

// Reconciliation handler (Pattern 3)
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		errorResponse(w, http.StatusBadRequest, "event_id query parameter is required")
		return
	}

	if s.postgres == nil {
		errorResponse(w, http.StatusServiceUnavailable, "PostgreSQL not configured — reconciliation requires a database connection")
		return
	}

	since := time.Now().Add(-24 * time.Hour)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if parsed, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = parsed
		}
	}

	fixed, err := s.svc.ReconcileReservations(eventID, since)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"event_id":         eventID,
		"since":            since.Format(time.RFC3339),
		"mismatches_fixed": fixed,
		"status":           "reconciliation complete",
	})
}
