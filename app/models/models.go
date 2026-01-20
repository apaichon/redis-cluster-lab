package models

import (
	"time"
)

// SeatStatus represents the status of a seat
type SeatStatus string

const (
	SeatAvailable SeatStatus = "available"
	SeatPending   SeatStatus = "pending"
	SeatReserved  SeatStatus = "reserved"
	SeatSold      SeatStatus = "sold"
)

// ReservationStatus represents the status of a reservation
type ReservationStatus string

const (
	ReservationPending   ReservationStatus = "pending"
	ReservationConfirmed ReservationStatus = "confirmed"
	ReservationCancelled ReservationStatus = "cancelled"
	ReservationExpired   ReservationStatus = "expired"
)

// Event represents a ticketed event (concert, show, etc.)
type Event struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Venue        string            `json:"venue"`
	Date         time.Time         `json:"date"`
	TotalSeats   int               `json:"total_seats"`
	Rows         int               `json:"rows"`
	SeatsPerRow  int               `json:"seats_per_row"`
	PricePerSeat float64           `json:"price_per_seat"`
	CreatedAt    time.Time         `json:"created_at"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Seat represents a single seat in an event venue
type Seat struct {
	ID       string     `json:"id"`       // e.g., "A1", "B15"
	EventID  string     `json:"event_id"`
	Row      string     `json:"row"`
	Number   int        `json:"number"`
	Status   SeatStatus `json:"status"`
	Price    float64    `json:"price"`
	HeldBy   string     `json:"held_by,omitempty"`
	HeldAt   *time.Time `json:"held_at,omitempty"`
	SoldTo   string     `json:"sold_to,omitempty"`
	SoldAt   *time.Time `json:"sold_at,omitempty"`
}

// Reservation represents a ticket reservation
type Reservation struct {
	ID            string            `json:"id"`
	EventID       string            `json:"event_id"`
	UserID        string            `json:"user_id"`
	Seats         []string          `json:"seats"`
	Status        ReservationStatus `json:"status"`
	TotalAmount   float64           `json:"total_amount"`
	CreatedAt     time.Time         `json:"created_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
	ConfirmedAt   *time.Time        `json:"confirmed_at,omitempty"`
	CancelledAt   *time.Time        `json:"cancelled_at,omitempty"`
	PaymentID     string            `json:"payment_id,omitempty"`
	CustomerEmail string            `json:"customer_email,omitempty"`
	CustomerName  string            `json:"customer_name,omitempty"`
}

// WaitlistEntry represents a user waiting for tickets
type WaitlistEntry struct {
	ID             string    `json:"id"`
	EventID        string    `json:"event_id"`
	UserID         string    `json:"user_id"`
	RequestedSeats int       `json:"requested_seats"`
	Email          string    `json:"email"`
	JoinedAt       time.Time `json:"joined_at"`
	NotifiedAt     *time.Time `json:"notified_at,omitempty"`
	Priority       int64     `json:"priority"` // Unix timestamp for FIFO ordering
}

// EventStats provides statistics for an event
type EventStats struct {
	EventID        string  `json:"event_id"`
	TotalSeats     int     `json:"total_seats"`
	AvailableSeats int     `json:"available_seats"`
	PendingSeats   int     `json:"pending_seats"`
	SoldSeats      int     `json:"sold_seats"`
	WaitlistCount  int     `json:"waitlist_count"`
	Revenue        float64 `json:"revenue"`
}

// ClusterNode represents a Redis cluster node
type ClusterNode struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Role     string `json:"role"` // master or slave
	MasterID string `json:"master_id,omitempty"`
	Slots    string `json:"slots,omitempty"`
}

// ClusterInfo provides cluster status information
type ClusterInfo struct {
	State         string        `json:"state"`
	SlotsAssigned int           `json:"slots_assigned"`
	SlotsOK       int           `json:"slots_ok"`
	SlotsPFail    int           `json:"slots_pfail"`
	SlotsFail     int           `json:"slots_fail"`
	KnownNodes    int           `json:"known_nodes"`
	Size          int           `json:"size"`
	Nodes         []ClusterNode `json:"nodes"`
}
