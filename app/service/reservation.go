package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"ticket-reservation/db"
	"ticket-reservation/models"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// Default reservation hold time (15 minutes)
	DefaultReservationTTL = 15 * time.Minute

	// Key patterns - using hash tags {event:ID} to ensure related keys are in the same slot
	eventKeyPattern        = "{event:%s}"           // Event metadata
	seatsKeyPattern        = "{event:%s}:seats"     // Hash of seat statuses
	reservationsKeyPattern = "{event:%s}:reservations" // Set of reservation IDs
	waitlistKeyPattern     = "{event:%s}:waitlist"  // Sorted set for waitlist
	reservationKeyPattern  = "reservation:%s"       // Individual reservation data
	userReservationsKey    = "user:%s:reservations" // User's reservations
	statsKeyPattern        = "{event:%s}:stats"     // Event statistics
)

// ReservationService handles ticket reservation operations
type ReservationService struct {
	rdb            *redis.ClusterClient
	postgres       *db.PostgresDB // optional, nil = Redis-only mode
	ctx            context.Context
	reservationTTL time.Duration
}

// NewReservationService creates a new reservation service
func NewReservationService(rdb *redis.ClusterClient, reservationTTL time.Duration) *ReservationService {
	if reservationTTL == 0 {
		reservationTTL = DefaultReservationTTL
	}
	return &ReservationService{
		rdb:            rdb,
		ctx:            context.Background(),
		reservationTTL: reservationTTL,
	}
}

// NewReservationServiceWithPG creates a new reservation service with PostgreSQL integration
func NewReservationServiceWithPG(rdb *redis.ClusterClient, pg *db.PostgresDB, reservationTTL time.Duration) *ReservationService {
	svc := NewReservationService(rdb, reservationTTL)
	svc.postgres = pg
	return svc
}

// CreateEvent creates a new event with a seat grid
// Pattern 1: Write-Through — writes to PostgreSQL first (source of truth), then Redis (cache)
func (s *ReservationService) CreateEvent(name, venue string, eventDate time.Time, rows, seatsPerRow int, pricePerSeat float64) (*models.Event, error) {
	eventID := uuid.New().String()[:8] // Short ID for readability

	event := &models.Event{
		ID:           eventID,
		Name:         name,
		Venue:        venue,
		Date:         eventDate,
		TotalSeats:   rows * seatsPerRow,
		Rows:         rows,
		SeatsPerRow:  seatsPerRow,
		PricePerSeat: pricePerSeat,
		CreatedAt:    time.Now(),
	}

	// === Write-Through: PostgreSQL first (source of truth) ===
	if s.postgres != nil {
		if err := s.postgres.InsertEvent(event); err != nil {
			return nil, fmt.Errorf("[PG] failed to insert event: %w", err)
		}
		log.Printf("[Write-Through] Event %s written to PostgreSQL", eventID)
	}

	// === Then write to Redis (cache) ===
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}

	pipe := s.rdb.Pipeline()
	pipe.Set(s.ctx, eventKey, eventJSON, 0)

	// Initialize seats as available
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	seatData := make(map[string]interface{})

	for row := 0; row < rows; row++ {
		rowLetter := string(rune('A' + row))
		for seatNum := 1; seatNum <= seatsPerRow; seatNum++ {
			seatID := fmt.Sprintf("%s%d", rowLetter, seatNum)
			seatData[seatID] = string(models.SeatAvailable)
		}
	}

	pipe.HSet(s.ctx, seatsKey, seatData)

	// Initialize stats
	statsKey := fmt.Sprintf(statsKeyPattern, eventID)
	pipe.HSet(s.ctx, statsKey, map[string]interface{}{
		"total_seats":     rows * seatsPerRow,
		"available_seats": rows * seatsPerRow,
		"pending_seats":   0,
		"sold_seats":      0,
		"revenue":         0,
	})

	_, err = pipe.Exec(s.ctx)
	if err != nil {
		if s.postgres != nil {
			log.Printf("[Write-Through] WARNING: Redis write failed for event %s, but PostgreSQL has the data: %v", eventID, err)
		} else {
			return nil, fmt.Errorf("failed to create event: %w", err)
		}
	} else {
		log.Printf("[Write-Through] Event %s written to Redis cache", eventID)
	}

	return event, nil
}

// GetEvent retrieves an event by ID
// Pattern 5: Fallback — tries Redis first, falls back to PostgreSQL
func (s *ReservationService) GetEvent(eventID string) (*models.Event, error) {
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)
	eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
	if err == nil {
		var event models.Event
		if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
		}
		return &event, nil
	}

	// Fallback to PostgreSQL
	if s.postgres != nil {
		log.Printf("[Fallback] Redis unavailable for event %s, falling back to PostgreSQL", eventID)
		pgEvent, pgErr := s.postgres.GetEvent(eventID)
		if pgErr == nil {
			return pgEvent, nil
		}
		return nil, fmt.Errorf("event not found in Redis or PostgreSQL: %s", eventID)
	}

	if err == redis.Nil {
		return nil, fmt.Errorf("event not found: %s", eventID)
	}
	return nil, fmt.Errorf("failed to get event: %w", err)
}

// ReserveSeats atomically reserves seats for a user
// Uses a Lua script to ensure atomicity in the cluster
func (s *ReservationService) ReserveSeats(eventID, userID string, seatIDs []string, customerName, customerEmail string) (*models.Reservation, error) {
	if len(seatIDs) == 0 {
		return nil, fmt.Errorf("no seats specified")
	}

	event, err := s.GetEvent(eventID)
	if err != nil {
		return nil, err
	}

	reservationID := uuid.New().String()[:12]
	now := time.Now()
	expiresAt := now.Add(s.reservationTTL)
	totalAmount := float64(len(seatIDs)) * event.PricePerSeat

	// Lua script for atomic seat reservation
	// All keys use the same hash tag {event:ID} so they're in the same slot
	reserveScript := redis.NewScript(`
		local seats_key = KEYS[1]
		local stats_key = KEYS[2]
		local reservation_id = ARGV[1]
		local user_id = ARGV[2]
		local expires_at = ARGV[3]
		local seat_count = tonumber(ARGV[4])

		-- Check all seats are available
		for i = 5, 4 + seat_count do
			local seat_id = ARGV[i]
			local status = redis.call('HGET', seats_key, seat_id)
			if status ~= 'available' then
				return {0, 'seat_unavailable', seat_id}
			end
		end

		-- Reserve all seats
		for i = 5, 4 + seat_count do
			local seat_id = ARGV[i]
			redis.call('HSET', seats_key, seat_id, 'pending')
		end

		-- Update stats
		redis.call('HINCRBY', stats_key, 'available_seats', -seat_count)
		redis.call('HINCRBY', stats_key, 'pending_seats', seat_count)

		return {1, reservation_id}
	`)

	// Build script arguments
	args := []interface{}{
		reservationID,
		userID,
		expiresAt.Unix(),
		len(seatIDs),
	}
	for _, seatID := range seatIDs {
		args = append(args, seatID)
	}

	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	statsKey := fmt.Sprintf(statsKeyPattern, eventID)

	result, err := reserveScript.Run(s.ctx, s.rdb, []string{seatsKey, statsKey}, args...).Slice()
	if err != nil {
		return nil, fmt.Errorf("failed to reserve seats: %w", err)
	}

	if result[0].(int64) == 0 {
		return nil, fmt.Errorf("seat %s is not available", result[2].(string))
	}

	// Create reservation record
	reservation := &models.Reservation{
		ID:            reservationID,
		EventID:       eventID,
		UserID:        userID,
		Seats:         seatIDs,
		Status:        models.ReservationPending,
		TotalAmount:   totalAmount,
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
		CustomerName:  customerName,
		CustomerEmail: customerEmail,
	}

	resJSON, _ := json.Marshal(reservation)
	resKey := fmt.Sprintf(reservationKeyPattern, reservationID)
	reservationsSetKey := fmt.Sprintf(reservationsKeyPattern, eventID)

	pipe := s.rdb.Pipeline()
	pipe.Set(s.ctx, resKey, resJSON, s.reservationTTL)
	pipe.SAdd(s.ctx, reservationsSetKey, reservationID)
	pipe.SAdd(s.ctx, fmt.Sprintf(userReservationsKey, userID), reservationID)

	_, err = pipe.Exec(s.ctx)
	if err != nil {
		// Rollback seats on failure
		s.releaseSeatsInternal(eventID, seatIDs)
		return nil, fmt.Errorf("failed to store reservation: %w", err)
	}

	// === Write-Through: Record pending reservation in PostgreSQL ===
	if s.postgres != nil {
		if pgErr := s.postgres.InsertReservation(reservation); pgErr != nil {
			log.Printf("[Write-Through] WARNING: PG write failed for reservation %s: %v", reservationID, pgErr)
			// Don't fail the operation — Redis has the data, reconciliation will catch up
		} else {
			log.Printf("[Write-Through] Reservation %s written to PostgreSQL", reservationID)
		}
	}

	return reservation, nil
}

// ConfirmReservation confirms a pending reservation (simulates payment)
func (s *ReservationService) ConfirmReservation(reservationID, paymentID string) (*models.Reservation, error) {
	resKey := fmt.Sprintf(reservationKeyPattern, reservationID)
	resJSON, err := s.rdb.Get(s.ctx, resKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("reservation not found or expired: %s", reservationID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get reservation: %w", err)
	}

	var reservation models.Reservation
	if err := json.Unmarshal([]byte(resJSON), &reservation); err != nil {
		return nil, fmt.Errorf("failed to unmarshal reservation: %w", err)
	}

	if reservation.Status != models.ReservationPending {
		return nil, fmt.Errorf("reservation is not pending: %s", reservation.Status)
	}

	// Confirm script - update seats to sold and update stats
	confirmScript := redis.NewScript(`
		local seats_key = KEYS[1]
		local stats_key = KEYS[2]
		local seat_count = tonumber(ARGV[1])
		local revenue = tonumber(ARGV[2])

		-- Update seats to sold
		for i = 3, 2 + seat_count do
			local seat_id = ARGV[i]
			redis.call('HSET', seats_key, seat_id, 'sold')
		end

		-- Update stats
		redis.call('HINCRBY', stats_key, 'pending_seats', -seat_count)
		redis.call('HINCRBY', stats_key, 'sold_seats', seat_count)
		redis.call('HINCRBYFLOAT', stats_key, 'revenue', revenue)

		return 1
	`)

	args := []interface{}{
		len(reservation.Seats),
		reservation.TotalAmount,
	}
	for _, seatID := range reservation.Seats {
		args = append(args, seatID)
	}

	seatsKey := fmt.Sprintf(seatsKeyPattern, reservation.EventID)
	statsKey := fmt.Sprintf(statsKeyPattern, reservation.EventID)

	_, err = confirmScript.Run(s.ctx, s.rdb, []string{seatsKey, statsKey}, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to confirm seats: %w", err)
	}

	// Update reservation
	now := time.Now()
	reservation.Status = models.ReservationConfirmed
	reservation.ConfirmedAt = &now
	reservation.PaymentID = paymentID

	resJSON2, _ := json.Marshal(reservation)
	s.rdb.Set(s.ctx, resKey, resJSON2, 0) // No expiry for confirmed reservations

	// === Write-Through: Update PostgreSQL ===
	if s.postgres != nil {
		if pgErr := s.postgres.UpdateReservationStatus(reservationID, models.ReservationConfirmed, paymentID); pgErr != nil {
			log.Printf("[Write-Through] WARNING: PG update failed for confirm %s: %v", reservationID, pgErr)
		}
		if pgErr := s.postgres.UpdateSeatStatuses(reservation.EventID, reservation.Seats, models.SeatSold, reservation.UserID); pgErr != nil {
			log.Printf("[Write-Through] WARNING: PG seat update failed for confirm %s: %v", reservationID, pgErr)
		}
		log.Printf("[Write-Through] Reservation %s confirmed in PostgreSQL", reservationID)
	}

	return &reservation, nil
}

// CancelReservation cancels a reservation and releases seats
func (s *ReservationService) CancelReservation(reservationID string) error {
	resKey := fmt.Sprintf(reservationKeyPattern, reservationID)
	resJSON, err := s.rdb.Get(s.ctx, resKey).Result()
	if err == redis.Nil {
		return fmt.Errorf("reservation not found: %s", reservationID)
	}
	if err != nil {
		return fmt.Errorf("failed to get reservation: %w", err)
	}

	var reservation models.Reservation
	if err := json.Unmarshal([]byte(resJSON), &reservation); err != nil {
		return fmt.Errorf("failed to unmarshal reservation: %w", err)
	}

	if reservation.Status == models.ReservationCancelled {
		return fmt.Errorf("reservation already cancelled")
	}

	// Release seats
	err = s.releaseSeatsInternal(reservation.EventID, reservation.Seats)
	if err != nil {
		return err
	}

	// Update reservation status
	now := time.Now()
	reservation.Status = models.ReservationCancelled
	reservation.CancelledAt = &now

	resJSON2, _ := json.Marshal(reservation)
	s.rdb.Set(s.ctx, resKey, resJSON2, 24*time.Hour) // Keep cancelled for 24h

	// === Write-Through: Update PostgreSQL ===
	if s.postgres != nil {
		if pgErr := s.postgres.UpdateReservationStatus(reservationID, models.ReservationCancelled, ""); pgErr != nil {
			log.Printf("[Write-Through] WARNING: PG update failed for cancel %s: %v", reservationID, pgErr)
		}
		if pgErr := s.postgres.UpdateSeatStatuses(reservation.EventID, reservation.Seats, models.SeatAvailable, ""); pgErr != nil {
			log.Printf("[Write-Through] WARNING: PG seat update failed for cancel %s: %v", reservationID, pgErr)
		}
		log.Printf("[Write-Through] Reservation %s cancelled in PostgreSQL", reservationID)
	}

	// Process waitlist
	go s.ProcessWaitlist(reservation.EventID, len(reservation.Seats))

	return nil
}

// releaseSeatsInternal releases seats back to available
func (s *ReservationService) releaseSeatsInternal(eventID string, seatIDs []string) error {
	releaseScript := redis.NewScript(`
		local seats_key = KEYS[1]
		local stats_key = KEYS[2]
		local seat_count = tonumber(ARGV[1])
		local released = 0

		for i = 2, 1 + seat_count do
			local seat_id = ARGV[i]
			local status = redis.call('HGET', seats_key, seat_id)
			if status == 'pending' then
				redis.call('HSET', seats_key, seat_id, 'available')
				released = released + 1
			end
		end

		if released > 0 then
			redis.call('HINCRBY', stats_key, 'pending_seats', -released)
			redis.call('HINCRBY', stats_key, 'available_seats', released)
		end

		return released
	`)

	args := []interface{}{len(seatIDs)}
	for _, seatID := range seatIDs {
		args = append(args, seatID)
	}

	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	statsKey := fmt.Sprintf(statsKeyPattern, eventID)

	_, err := releaseScript.Run(s.ctx, s.rdb, []string{seatsKey, statsKey}, args...).Result()
	return err
}

// GetAvailability returns event availability statistics
// Pattern 5: Fallback — tries Redis first, falls back to PostgreSQL
func (s *ReservationService) GetAvailability(eventID string) (*models.EventStats, error) {
	statsKey := fmt.Sprintf(statsKeyPattern, eventID)
	waitlistKey := fmt.Sprintf(waitlistKeyPattern, eventID)

	pipe := s.rdb.Pipeline()
	statsCmd := pipe.HGetAll(s.ctx, statsKey)
	waitlistCmd := pipe.ZCard(s.ctx, waitlistKey)

	_, err := pipe.Exec(s.ctx)
	if err != nil || len(statsCmd.Val()) == 0 {
		// Fallback to PostgreSQL
		if s.postgres != nil {
			log.Printf("[Fallback] Redis unavailable for stats %s, falling back to PostgreSQL", eventID)
			return s.postgres.GetEventStats(eventID)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get availability: %w", err)
		}
		return nil, fmt.Errorf("event not found: %s", eventID)
	}

	statsMap := statsCmd.Val()
	totalSeats, _ := strconv.Atoi(statsMap["total_seats"])
	availableSeats, _ := strconv.Atoi(statsMap["available_seats"])
	pendingSeats, _ := strconv.Atoi(statsMap["pending_seats"])
	soldSeats, _ := strconv.Atoi(statsMap["sold_seats"])
	revenue, _ := strconv.ParseFloat(statsMap["revenue"], 64)

	return &models.EventStats{
		EventID:        eventID,
		TotalSeats:     totalSeats,
		AvailableSeats: availableSeats,
		PendingSeats:   pendingSeats,
		SoldSeats:      soldSeats,
		WaitlistCount:  int(waitlistCmd.Val()),
		Revenue:        revenue,
	}, nil
}

// GetAvailableSeats returns a list of available seat IDs
func (s *ReservationService) GetAvailableSeats(eventID string) ([]string, error) {
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	seatsMap, err := s.rdb.HGetAll(s.ctx, seatsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get seats: %w", err)
	}

	var available []string
	for seatID, status := range seatsMap {
		if status == string(models.SeatAvailable) {
			available = append(available, seatID)
		}
	}

	return available, nil
}

// JoinWaitlist adds a user to the event waitlist
func (s *ReservationService) JoinWaitlist(eventID, userID, email string, requestedSeats int) (*models.WaitlistEntry, error) {
	entry := &models.WaitlistEntry{
		ID:             uuid.New().String()[:8],
		EventID:        eventID,
		UserID:         userID,
		RequestedSeats: requestedSeats,
		Email:          email,
		JoinedAt:       time.Now(),
		Priority:       time.Now().UnixNano(), // FIFO ordering
	}

	waitlistKey := fmt.Sprintf(waitlistKeyPattern, eventID)
	entryJSON, _ := json.Marshal(entry)

	// Add to sorted set with timestamp as score
	err := s.rdb.ZAdd(s.ctx, waitlistKey, redis.Z{
		Score:  float64(entry.Priority),
		Member: string(entryJSON),
	}).Err()

	if err != nil {
		return nil, fmt.Errorf("failed to join waitlist: %w", err)
	}

	return entry, nil
}

// ProcessWaitlist processes waitlist when seats become available
func (s *ReservationService) ProcessWaitlist(eventID string, availableSeats int) {
	if availableSeats <= 0 {
		return
	}

	waitlistKey := fmt.Sprintf(waitlistKeyPattern, eventID)

	// Get entries from waitlist (FIFO order)
	entries, err := s.rdb.ZRange(s.ctx, waitlistKey, 0, -1).Result()
	if err != nil || len(entries) == 0 {
		return
	}

	for _, entryJSON := range entries {
		var entry models.WaitlistEntry
		if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
			continue
		}

		if entry.RequestedSeats <= availableSeats {
			// Notify user (in production, send email)
			fmt.Printf("[WAITLIST] Notifying %s: %d seats available for event %s\n",
				entry.Email, entry.RequestedSeats, eventID)

			// Remove from waitlist
			s.rdb.ZRem(s.ctx, waitlistKey, entryJSON)
			availableSeats -= entry.RequestedSeats
		}

		if availableSeats <= 0 {
			break
		}
	}
}

// GetReservation retrieves a reservation by ID
// Pattern 5: Fallback — tries Redis first, falls back to PostgreSQL
func (s *ReservationService) GetReservation(reservationID string) (*models.Reservation, error) {
	resKey := fmt.Sprintf(reservationKeyPattern, reservationID)
	resJSON, err := s.rdb.Get(s.ctx, resKey).Result()
	if err == nil {
		var reservation models.Reservation
		if err := json.Unmarshal([]byte(resJSON), &reservation); err != nil {
			return nil, fmt.Errorf("failed to unmarshal reservation: %w", err)
		}
		return &reservation, nil
	}

	// Fallback to PostgreSQL
	if s.postgres != nil {
		log.Printf("[Fallback] Redis unavailable for reservation %s, falling back to PostgreSQL", reservationID)
		return s.postgres.GetReservation(reservationID)
	}

	if err == redis.Nil {
		return nil, fmt.Errorf("reservation not found: %s", reservationID)
	}
	return nil, fmt.Errorf("failed to get reservation: %w", err)
}

// GetUserReservations retrieves all reservations for a user
func (s *ReservationService) GetUserReservations(userID string) ([]*models.Reservation, error) {
	userResKey := fmt.Sprintf(userReservationsKey, userID)
	resIDs, err := s.rdb.SMembers(s.ctx, userResKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get user reservations: %w", err)
	}

	var reservations []*models.Reservation
	for _, resID := range resIDs {
		res, err := s.GetReservation(resID)
		if err == nil {
			reservations = append(reservations, res)
		}
	}

	return reservations, nil
}

// PrintSeatMap displays the current seat map
func (s *ReservationService) PrintSeatMap(eventID string) error {
	event, err := s.GetEvent(eventID)
	if err != nil {
		return err
	}

	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	seatsMap, err := s.rdb.HGetAll(s.ctx, seatsKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get seats: %w", err)
	}

	fmt.Printf("\n=== Seat Map: %s ===\n", event.Name)
	fmt.Printf("    ")
	for i := 1; i <= event.SeatsPerRow; i++ {
		fmt.Printf("%3d", i)
	}
	fmt.Println()

	for row := 0; row < event.Rows; row++ {
		rowLetter := string(rune('A' + row))
		fmt.Printf(" %s  ", rowLetter)
		for seatNum := 1; seatNum <= event.SeatsPerRow; seatNum++ {
			seatID := fmt.Sprintf("%s%d", rowLetter, seatNum)
			status := seatsMap[seatID]
			symbol := "O" // available
			switch status {
			case string(models.SeatPending):
				symbol = "P"
			case string(models.SeatReserved):
				symbol = "R"
			case string(models.SeatSold):
				symbol = "X"
			}
			fmt.Printf("  %s", symbol)
		}
		fmt.Println()
	}
	fmt.Println("\nLegend: O=Available, P=Pending, R=Reserved, X=Sold")
	fmt.Println(strings.Repeat("=", 40))

	return nil
}

// ReconcileReservations syncs PostgreSQL confirmed seats to Redis (Pattern 3: Periodic Reconciliation)
func (s *ReservationService) ReconcileReservations(eventID string, since time.Time) (int, error) {
	if s.postgres == nil {
		return 0, fmt.Errorf("PostgreSQL not configured — reconciliation requires a database connection")
	}

	log.Printf("[Reconciliation] Starting reconciliation for event %s since %s", eventID, since.Format(time.RFC3339))

	// Get confirmed seats from PostgreSQL since last sync
	confirmedSeats, err := s.postgres.GetConfirmedSeatsSince(eventID, since)
	if err != nil {
		return 0, fmt.Errorf("failed to get confirmed seats from PostgreSQL: %w", err)
	}

	if len(confirmedSeats) == 0 {
		log.Printf("[Reconciliation] No seats to reconcile")
		return 0, nil
	}

	// Check each seat in Redis and fix mismatches
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	fixed := 0

	for _, seat := range confirmedSeats {
		// Get current Redis status
		redisStatus, err := s.rdb.HGet(s.ctx, seatsKey, seat.SeatID).Result()
		if err != nil {
			log.Printf("[Reconciliation] WARNING: Could not read Redis seat %s: %v", seat.SeatID, err)
			continue
		}

		// If Redis doesn't show this seat as sold, fix it
		if redisStatus != string(models.SeatSold) {
			log.Printf("[Reconciliation] MISMATCH: Seat %s is '%s' in Redis but confirmed in PG (reservation %s). Fixing...",
				seat.SeatID, redisStatus, seat.ReservationID)

			err = s.rdb.HSet(s.ctx, seatsKey, seat.SeatID, string(models.SeatSold)).Err()
			if err != nil {
				log.Printf("[Reconciliation] ERROR: Failed to fix seat %s in Redis: %v", seat.SeatID, err)
				continue
			}
			fixed++
		}
	}

	log.Printf("[Reconciliation] Complete: checked %d seats, fixed %d mismatches", len(confirmedSeats), fixed)
	return fixed, nil
}

// CleanupExpiredReservations cleans up expired pending reservations
func (s *ReservationService) CleanupExpiredReservations(eventID string) (int, error) {
	reservationsKey := fmt.Sprintf(reservationsKeyPattern, eventID)
	resIDs, err := s.rdb.SMembers(s.ctx, reservationsKey).Result()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, resID := range resIDs {
		res, err := s.GetReservation(resID)
		if err != nil {
			// Reservation expired (TTL)
			s.rdb.SRem(s.ctx, reservationsKey, resID)
			cleaned++
			continue
		}

		if res.Status == models.ReservationPending && time.Now().After(res.ExpiresAt) {
			s.releaseSeatsInternal(eventID, res.Seats)
			s.rdb.SRem(s.ctx, reservationsKey, resID)
			cleaned++
		}
	}

	return cleaned, nil
}
