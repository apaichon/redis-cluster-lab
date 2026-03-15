package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"ticket-reservation/models"

	_ "github.com/lib/pq"
)

// PostgresDB wraps a PostgreSQL connection with ticket reservation operations
type PostgresDB struct {
	DB *sql.DB
}

// NewPostgresDB connects to PostgreSQL and initializes the schema
func NewPostgresDB(dsn string) (*PostgresDB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection with retries
	for i := 0; i < 5; i++ {
		err = db.Ping()
		if err == nil {
			break
		}
		time.Sleep(time.Second * time.Duration(i+1))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	pg := &PostgresDB{DB: db}

	// Run schema migration
	if err := pg.InitSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	log.Println("[PostgreSQL] Connected and schema initialized")
	return pg, nil
}

// InitSchema creates the database tables if they don't exist
func (pg *PostgresDB) InitSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS events (
		id            VARCHAR(36) PRIMARY KEY,
		name          VARCHAR(255) NOT NULL,
		venue         VARCHAR(255) NOT NULL,
		event_date    TIMESTAMP NOT NULL,
		total_seats   INTEGER NOT NULL,
		rows          INTEGER NOT NULL,
		seats_per_row INTEGER NOT NULL,
		price_per_seat NUMERIC(10,2) NOT NULL,
		created_at    TIMESTAMP NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS seats (
		event_id VARCHAR(36) NOT NULL REFERENCES events(id),
		seat_id  VARCHAR(10) NOT NULL,
		row_letter VARCHAR(1) NOT NULL,
		seat_number INTEGER NOT NULL,
		status   VARCHAR(20) NOT NULL DEFAULT 'available',
		price    NUMERIC(10,2) NOT NULL,
		held_by  VARCHAR(36),
		sold_to  VARCHAR(36),
		updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
		PRIMARY KEY (event_id, seat_id)
	);

	CREATE TABLE IF NOT EXISTS reservations (
		id             VARCHAR(36) PRIMARY KEY,
		event_id       VARCHAR(36) NOT NULL REFERENCES events(id),
		user_id        VARCHAR(36) NOT NULL,
		status         VARCHAR(20) NOT NULL DEFAULT 'pending',
		total_amount   NUMERIC(10,2) NOT NULL,
		customer_name  VARCHAR(255),
		customer_email VARCHAR(255),
		payment_id     VARCHAR(100),
		created_at     TIMESTAMP NOT NULL DEFAULT NOW(),
		expires_at     TIMESTAMP NOT NULL,
		confirmed_at   TIMESTAMP,
		cancelled_at   TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS reservation_seats (
		reservation_id VARCHAR(36) NOT NULL REFERENCES reservations(id),
		event_id       VARCHAR(36) NOT NULL,
		seat_id        VARCHAR(10) NOT NULL,
		PRIMARY KEY (reservation_id, seat_id),
		FOREIGN KEY (event_id, seat_id) REFERENCES seats(event_id, seat_id)
	);

	CREATE INDEX IF NOT EXISTS idx_seats_event_status ON seats(event_id, status);
	CREATE INDEX IF NOT EXISTS idx_reservations_event ON reservations(event_id);
	CREATE INDEX IF NOT EXISTS idx_reservations_user ON reservations(user_id);
	CREATE INDEX IF NOT EXISTS idx_reservations_status ON reservations(status);
	CREATE INDEX IF NOT EXISTS idx_reservation_seats_event ON reservation_seats(event_id, seat_id);
	`

	_, err := pg.DB.Exec(schema)
	return err
}

// InsertEvent inserts an event and its seats into PostgreSQL
func (pg *PostgresDB) InsertEvent(event *models.Event) error {
	tx, err := pg.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert event
	_, err = tx.Exec(`
		INSERT INTO events (id, name, venue, event_date, total_seats, rows, seats_per_row, price_per_seat, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		event.ID, event.Name, event.Venue, event.Date,
		event.TotalSeats, event.Rows, event.SeatsPerRow, event.PricePerSeat, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}

	// Insert seats
	stmt, err := tx.Prepare(`
		INSERT INTO seats (event_id, seat_id, row_letter, seat_number, status, price, updated_at)
		VALUES ($1, $2, $3, $4, 'available', $5, NOW())
		ON CONFLICT (event_id, seat_id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("failed to prepare seat insert: %w", err)
	}
	defer stmt.Close()

	for row := 0; row < event.Rows; row++ {
		rowLetter := string(rune('A' + row))
		for seatNum := 1; seatNum <= event.SeatsPerRow; seatNum++ {
			seatID := fmt.Sprintf("%s%d", rowLetter, seatNum)
			_, err = stmt.Exec(event.ID, seatID, rowLetter, seatNum, event.PricePerSeat)
			if err != nil {
				return fmt.Errorf("failed to insert seat %s: %w", seatID, err)
			}
		}
	}

	return tx.Commit()
}

// InsertReservation inserts a reservation and its seat mappings
func (pg *PostgresDB) InsertReservation(res *models.Reservation) error {
	tx, err := pg.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO reservations (id, event_id, user_id, status, total_amount, customer_name, customer_email, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		res.ID, res.EventID, res.UserID, string(res.Status),
		res.TotalAmount, res.CustomerName, res.CustomerEmail,
		res.CreatedAt, res.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert reservation: %w", err)
	}

	// Insert reservation_seats and update seat statuses
	for _, seatID := range res.Seats {
		_, err = tx.Exec(`
			INSERT INTO reservation_seats (reservation_id, event_id, seat_id)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`,
			res.ID, res.EventID, seatID,
		)
		if err != nil {
			return fmt.Errorf("failed to insert reservation seat: %w", err)
		}

		_, err = tx.Exec(`
			UPDATE seats SET status = 'pending', held_by = $1, updated_at = NOW()
			WHERE event_id = $2 AND seat_id = $3`,
			res.UserID, res.EventID, seatID,
		)
		if err != nil {
			return fmt.Errorf("failed to update seat status: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateReservationStatus updates a reservation's status in PostgreSQL
func (pg *PostgresDB) UpdateReservationStatus(reservationID string, status models.ReservationStatus, paymentID string) error {
	var err error
	switch status {
	case models.ReservationConfirmed:
		_, err = pg.DB.Exec(`
			UPDATE reservations SET status = $1, payment_id = $2, confirmed_at = NOW()
			WHERE id = $3`,
			string(status), paymentID, reservationID,
		)
	case models.ReservationCancelled:
		_, err = pg.DB.Exec(`
			UPDATE reservations SET status = $1, cancelled_at = NOW()
			WHERE id = $2`,
			string(status), reservationID,
		)
	default:
		_, err = pg.DB.Exec(`
			UPDATE reservations SET status = $1 WHERE id = $2`,
			string(status), reservationID,
		)
	}
	return err
}

// UpdateSeatStatuses updates seat statuses for an event in batch
func (pg *PostgresDB) UpdateSeatStatuses(eventID string, seatIDs []string, status models.SeatStatus, userID string) error {
	if len(seatIDs) == 0 {
		return nil
	}

	// Build placeholder list
	placeholders := make([]string, len(seatIDs))
	args := make([]interface{}, 0, len(seatIDs)+3)
	args = append(args, string(status), eventID)

	for i, seatID := range seatIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+3)
		args = append(args, seatID)
	}

	query := fmt.Sprintf(`
		UPDATE seats SET status = $1, updated_at = NOW()
		WHERE event_id = $2 AND seat_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	if status == models.SeatSold && userID != "" {
		// Add sold_to field for sold status
		query = fmt.Sprintf(`
			UPDATE seats SET status = $1, sold_to = '%s', updated_at = NOW()
			WHERE event_id = $2 AND seat_id IN (%s)`,
			userID, strings.Join(placeholders, ","),
		)
	}

	_, err := pg.DB.Exec(query, args...)
	return err
}

// GetSeats returns all seats for an event (fallback read)
func (pg *PostgresDB) GetSeats(eventID string) (map[string]string, error) {
	rows, err := pg.DB.Query(`
		SELECT seat_id, status FROM seats WHERE event_id = $1`,
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seats := make(map[string]string)
	for rows.Next() {
		var seatID, status string
		if err := rows.Scan(&seatID, &status); err != nil {
			return nil, err
		}
		seats[seatID] = status
	}
	return seats, rows.Err()
}

// GetEvent retrieves an event from PostgreSQL (fallback read)
func (pg *PostgresDB) GetEvent(eventID string) (*models.Event, error) {
	event := &models.Event{}
	err := pg.DB.QueryRow(`
		SELECT id, name, venue, event_date, total_seats, rows, seats_per_row, price_per_seat, created_at
		FROM events WHERE id = $1`,
		eventID,
	).Scan(
		&event.ID, &event.Name, &event.Venue, &event.Date,
		&event.TotalSeats, &event.Rows, &event.SeatsPerRow, &event.PricePerSeat, &event.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("event not found in PostgreSQL: %s", eventID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get event from PostgreSQL: %w", err)
	}
	return event, nil
}

// GetReservation retrieves a reservation from PostgreSQL (fallback read)
func (pg *PostgresDB) GetReservation(reservationID string) (*models.Reservation, error) {
	res := &models.Reservation{}
	var status string
	var confirmedAt, cancelledAt sql.NullTime
	var paymentID, customerName, customerEmail sql.NullString

	err := pg.DB.QueryRow(`
		SELECT id, event_id, user_id, status, total_amount, customer_name, customer_email,
		       payment_id, created_at, expires_at, confirmed_at, cancelled_at
		FROM reservations WHERE id = $1`,
		reservationID,
	).Scan(
		&res.ID, &res.EventID, &res.UserID, &status, &res.TotalAmount,
		&customerName, &customerEmail, &paymentID,
		&res.CreatedAt, &res.ExpiresAt, &confirmedAt, &cancelledAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reservation not found in PostgreSQL: %s", reservationID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get reservation from PostgreSQL: %w", err)
	}

	res.Status = models.ReservationStatus(status)
	if confirmedAt.Valid {
		res.ConfirmedAt = &confirmedAt.Time
	}
	if cancelledAt.Valid {
		res.CancelledAt = &cancelledAt.Time
	}
	if paymentID.Valid {
		res.PaymentID = paymentID.String
	}
	if customerName.Valid {
		res.CustomerName = customerName.String
	}
	if customerEmail.Valid {
		res.CustomerEmail = customerEmail.String
	}

	// Get seat IDs
	rows, err := pg.DB.Query(`
		SELECT seat_id FROM reservation_seats WHERE reservation_id = $1`,
		reservationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var seatID string
		if err := rows.Scan(&seatID); err != nil {
			return nil, err
		}
		res.Seats = append(res.Seats, seatID)
	}

	return res, rows.Err()
}

// GetConfirmedSeatsSince returns seats confirmed after a given time (for reconciliation)
func (pg *PostgresDB) GetConfirmedSeatsSince(eventID string, since time.Time) ([]SeatSync, error) {
	rows, err := pg.DB.Query(`
		SELECT s.seat_id, s.status, r.id as reservation_id
		FROM seats s
		JOIN reservation_seats rs ON rs.event_id = s.event_id AND rs.seat_id = s.seat_id
		JOIN reservations r ON r.id = rs.reservation_id
		WHERE s.event_id = $1
		  AND r.status = 'confirmed'
		  AND r.confirmed_at >= $2`,
		eventID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SeatSync
	for rows.Next() {
		var ss SeatSync
		if err := rows.Scan(&ss.SeatID, &ss.Status, &ss.ReservationID); err != nil {
			return nil, err
		}
		results = append(results, ss)
	}
	return results, rows.Err()
}

// SeatSync holds seat synchronization data for reconciliation
type SeatSync struct {
	SeatID        string
	Status        string
	ReservationID string
}

// GetEventStats returns event statistics from PostgreSQL (fallback)
func (pg *PostgresDB) GetEventStats(eventID string) (*models.EventStats, error) {
	stats := &models.EventStats{EventID: eventID}

	err := pg.DB.QueryRow(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'available') as available,
			COUNT(*) FILTER (WHERE status = 'pending') as pending,
			COUNT(*) FILTER (WHERE status = 'sold') as sold,
			COALESCE(SUM(price) FILTER (WHERE status = 'sold'), 0) as revenue
		FROM seats WHERE event_id = $1`,
		eventID,
	).Scan(&stats.TotalSeats, &stats.AvailableSeats, &stats.PendingSeats, &stats.SoldSeats, &stats.Revenue)

	return stats, err
}

// Close closes the PostgreSQL connection
func (pg *PostgresDB) Close() error {
	log.Println("[PostgreSQL] Connection closed")
	return pg.DB.Close()
}
