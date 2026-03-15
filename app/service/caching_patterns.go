package service

// Caching Patterns — Part 7 Lab Demonstrations
// This file contains all 6 caching pattern implementations referenced in
// docs/Part7-Caching-Patterns-Complete-Guide.md

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"ticket-reservation/db"
	"ticket-reservation/models"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// Pattern 1: Cache-Aside (Lazy Loading)
// ============================================================================

// GetEventCacheAside demonstrates the Cache-Aside pattern for GetEvent.
// The APPLICATION is responsible for managing the cache.
func (s *ReservationService) GetEventCacheAside(eventID string) (*models.Event, error) {
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)

	// Step 1: Check Redis cache
	eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
	if err == nil {
		// CACHE HIT — deserialize and return
		var event models.Event
		if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
		}
		log.Printf("[Cache-Aside] HIT for event %s", eventID)
		return &event, nil
	}

	if s.postgres == nil {
		return nil, fmt.Errorf("cache miss and no PostgreSQL configured")
	}

	// Step 2: CACHE MISS — query PostgreSQL
	log.Printf("[Cache-Aside] MISS for event %s — loading from PostgreSQL", eventID)
	event, pgErr := s.postgres.GetEvent(eventID)
	if pgErr != nil {
		return nil, fmt.Errorf("event not found: %w", pgErr)
	}

	// Step 3: Populate cache for future reads (with TTL)
	data, _ := json.Marshal(event)
	s.rdb.Set(s.ctx, eventKey, data, 1*time.Hour)
	log.Printf("[Cache-Aside] Cached event %s (TTL: 1 hour)", eventID)

	// Step 4: Return data
	return event, nil
}

// GetSeatsCacheAside demonstrates Cache-Aside with Hash type for seat statuses.
func (s *ReservationService) GetSeatsCacheAside(eventID string) (map[string]string, error) {
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)

	// Step 1: Check Redis
	seatsMap, err := s.rdb.HGetAll(s.ctx, seatsKey).Result()
	if err == nil && len(seatsMap) > 0 {
		log.Printf("[Cache-Aside] HIT — %d seats from Redis", len(seatsMap))
		return seatsMap, nil
	}

	if s.postgres == nil {
		return nil, fmt.Errorf("cache miss and no PostgreSQL configured")
	}

	// Step 2: Cache miss — load from PostgreSQL
	log.Printf("[Cache-Aside] MISS — loading seats from PostgreSQL")
	seats, pgErr := s.postgres.GetSeats(eventID)
	if pgErr != nil {
		return nil, pgErr
	}

	// Step 3: Populate Redis hash
	seatData := make(map[string]interface{})
	for k, v := range seats {
		seatData[k] = v
	}
	s.rdb.HSet(s.ctx, seatsKey, seatData)
	s.rdb.Expire(s.ctx, seatsKey, 30*time.Minute)

	return seats, nil
}

// UpdateEventCacheAside demonstrates Cache Invalidation with Cache-Aside.
// When data changes, INVALIDATE the cache (don't update it).
func (s *ReservationService) UpdateEventCacheAside(eventID string, name string) error {
	if s.postgres == nil {
		return fmt.Errorf("PostgreSQL not configured")
	}

	// Step 1: Update PostgreSQL (source of truth)
	_, err := s.postgres.DB.Exec(
		`UPDATE events SET name = $1 WHERE id = $2`, name, eventID,
	)
	if err != nil {
		return err
	}

	// Step 2: DELETE from cache (next read will re-populate)
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)
	s.rdb.Del(s.ctx, eventKey)
	log.Printf("[Cache-Aside] Invalidated cache for event %s", eventID)

	return nil
}

// ============================================================================
// Pattern 2: Read-Through
// ============================================================================

// ReadThroughCache wraps Redis with automatic PostgreSQL loading.
// The caller never touches PostgreSQL directly.
type ReadThroughCache struct {
	rdb      *redis.ClusterClient
	postgres *db.PostgresDB
	ctx      context.Context
}

// NewReadThroughCache creates a new Read-Through cache wrapper.
func NewReadThroughCache(rdb *redis.ClusterClient, pg *db.PostgresDB) *ReadThroughCache {
	return &ReadThroughCache{
		rdb:      rdb,
		postgres: pg,
		ctx:      context.Background(),
	}
}

// GetEvent implements the Read-Through pattern.
// The caller never touches PostgreSQL directly.
func (c *ReadThroughCache) GetEvent(eventID string) (*models.Event, error) {
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)

	// The cache handles everything
	eventJSON, err := c.rdb.Get(c.ctx, eventKey).Result()
	if err == redis.Nil {
		// Cache auto-loads from DB (transparent to caller)
		return c.loadAndCache(eventID, eventKey)
	}
	if err != nil {
		return nil, err
	}

	var event models.Event
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		return nil, err
	}
	log.Printf("[Read-Through] HIT for event %s", eventID)
	return &event, nil
}

// loadAndCache is INTERNAL to the cache — caller doesn't know about it.
func (c *ReadThroughCache) loadAndCache(eventID, cacheKey string) (*models.Event, error) {
	// Load from PostgreSQL
	event, err := c.postgres.GetEvent(eventID)
	if err != nil {
		return nil, err
	}

	// Store in cache with TTL
	data, _ := json.Marshal(event)
	c.rdb.Set(c.ctx, cacheKey, data, 1*time.Hour)
	log.Printf("[Read-Through] Auto-loaded event %s into cache", eventID)

	return event, nil
}

// GetEventStats implements Read-Through for expensive computed data.
func (c *ReadThroughCache) GetEventStats(eventID string) (*models.EventStats, error) {
	cacheKey := fmt.Sprintf("{event:%s}:computed_stats", eventID)

	// Try cache first
	data, err := c.rdb.Get(c.ctx, cacheKey).Result()
	if err == nil {
		var stats models.EventStats
		if err := json.Unmarshal([]byte(data), &stats); err != nil {
			return nil, err
		}
		log.Printf("[Read-Through] HIT for stats %s", eventID)
		return &stats, nil
	}

	// Auto-load: compute from PostgreSQL (expensive query)
	stats, pgErr := c.postgres.GetEventStats(eventID)
	if pgErr != nil {
		return nil, pgErr
	}

	// Cache computed result (shorter TTL for dynamic data)
	jsonData, _ := json.Marshal(stats)
	c.rdb.Set(c.ctx, cacheKey, jsonData, 5*time.Minute)
	log.Printf("[Read-Through] Cached computed stats for event %s", eventID)

	return stats, nil
}

// ============================================================================
// Pattern 3: Refresh-Ahead (Predictive Refresh)
// ============================================================================

const (
	eventCacheTTL    = 10 * time.Minute
	refreshThreshold = 0.7 // Refresh when 70% of TTL has elapsed
)

// GetEventRefreshAhead reads with proactive refresh.
// When TTL drops below threshold, triggers a background refresh.
func (s *ReservationService) GetEventRefreshAhead(eventID string) (*models.Event, error) {
	eventKey := fmt.Sprintf(eventKeyPattern, eventID)

	// Step 1: Get value from Redis
	eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
	if err == redis.Nil {
		// Cold miss — load from PostgreSQL
		return s.loadEventIntoCache(eventID, eventKey)
	}
	if err != nil {
		return nil, err
	}

	// Step 2: Check remaining TTL
	ttl, _ := s.rdb.TTL(s.ctx, eventKey).Result()
	if ttl > 0 {
		remainingRatio := float64(ttl) / float64(eventCacheTTL)

		// Step 3: If TTL is below threshold, trigger background refresh
		if remainingRatio < (1 - refreshThreshold) {
			log.Printf("[Refresh-Ahead] TTL at %.0f%% for event %s — triggering background refresh",
				remainingRatio*100, eventID)
			go s.refreshEventCache(eventID, eventKey) // Non-blocking!
		}
	}

	// Step 4: Return cached data immediately (always fast)
	var event models.Event
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// refreshEventCache reloads from PostgreSQL in the background.
func (s *ReservationService) refreshEventCache(eventID, cacheKey string) {
	if s.postgres == nil {
		return
	}
	event, err := s.postgres.GetEvent(eventID)
	if err != nil {
		log.Printf("[Refresh-Ahead] Background refresh failed for %s: %v", eventID, err)
		return
	}

	data, _ := json.Marshal(event)
	s.rdb.Set(s.ctx, cacheKey, data, eventCacheTTL)
	log.Printf("[Refresh-Ahead] Refreshed event %s (new TTL: %v)", eventID, eventCacheTTL)
}

// loadEventIntoCache handles cold cache miss.
func (s *ReservationService) loadEventIntoCache(eventID, cacheKey string) (*models.Event, error) {
	if s.postgres == nil {
		return nil, fmt.Errorf("cache miss and no PostgreSQL configured")
	}
	event, err := s.postgres.GetEvent(eventID)
	if err != nil {
		return nil, err
	}

	data, _ := json.Marshal(event)
	s.rdb.Set(s.ctx, cacheKey, data, eventCacheTTL)
	log.Printf("[Refresh-Ahead] Cold load event %s into cache", eventID)

	return event, nil
}

// RefreshAheadWorker continuously monitors and refreshes hot keys.
type RefreshAheadWorker struct {
	rdb     *redis.ClusterClient
	ctx     context.Context
	cancel  context.CancelFunc
	hotKeys map[string]RefreshConfig
}

// RefreshConfig defines refresh parameters for a key.
type RefreshConfig struct {
	TTL       time.Duration
	Threshold float64 // 0.0 to 1.0
	Loader    func(key string) ([]byte, error)
}

// NewRefreshAheadWorker creates a new refresh-ahead worker.
func NewRefreshAheadWorker(rdb *redis.ClusterClient) *RefreshAheadWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &RefreshAheadWorker{
		rdb:     rdb,
		ctx:     ctx,
		cancel:  cancel,
		hotKeys: make(map[string]RefreshConfig),
	}
}

// Register adds a key to be monitored for refresh.
func (w *RefreshAheadWorker) Register(key string, config RefreshConfig) {
	w.hotKeys[key] = config
}

// Start begins the refresh-ahead monitoring loop.
func (w *RefreshAheadWorker) Start() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.checkAndRefresh()
		case <-w.ctx.Done():
			return
		}
	}
}

// Stop stops the worker.
func (w *RefreshAheadWorker) Stop() {
	w.cancel()
}

func (w *RefreshAheadWorker) checkAndRefresh() {
	for key, config := range w.hotKeys {
		ttl, err := w.rdb.TTL(w.ctx, key).Result()
		if err != nil || ttl < 0 {
			continue
		}

		remainingRatio := float64(ttl) / float64(config.TTL)
		if remainingRatio < (1-config.Threshold) && remainingRatio > 0 {
			data, err := config.Loader(key)
			if err == nil {
				w.rdb.Set(w.ctx, key, data, config.TTL)
				log.Printf("[Refresh-Ahead Worker] Refreshed key %s", key)
			}
		}
	}
}

// ============================================================================
// Pattern 5: Write-Behind / Write-Back (Async Writes)
// ============================================================================

// WriteBehindBuffer collects writes and flushes them in batches to PostgreSQL.
type WriteBehindBuffer struct {
	rdb      *redis.ClusterClient
	postgres *db.PostgresDB
	ctx      context.Context
	cancel   context.CancelFunc
	buffer   chan WriteOp
	wg       sync.WaitGroup
}

// WriteOp represents a single write operation to be flushed.
type WriteOp struct {
	Type      string // "seat_update", "reservation_insert", etc.
	EventID   string
	SeatID    string
	Status    string
	Timestamp time.Time
}

// NewWriteBehindBuffer creates a new write-behind buffer with a background flusher.
func NewWriteBehindBuffer(rdb *redis.ClusterClient, pg *db.PostgresDB, bufferSize int) *WriteBehindBuffer {
	ctx, cancel := context.WithCancel(context.Background())
	wb := &WriteBehindBuffer{
		rdb:      rdb,
		postgres: pg,
		ctx:      ctx,
		cancel:   cancel,
		buffer:   make(chan WriteOp, bufferSize),
	}
	// Start background flusher
	wb.wg.Add(1)
	go wb.flushLoop()
	return wb
}

// UpdateSeatStatus writes to Redis immediately, queues PG write asynchronously.
func (wb *WriteBehindBuffer) UpdateSeatStatus(eventID, seatID, status string) error {
	// Step 1: Write to Redis IMMEDIATELY (fast path)
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)
	err := wb.rdb.HSet(wb.ctx, seatsKey, seatID, status).Err()
	if err != nil {
		return err
	}
	log.Printf("[Write-Behind] Redis updated: %s/%s = %s", eventID, seatID, status)

	// Step 2: Queue for async PostgreSQL write (non-blocking)
	wb.buffer <- WriteOp{
		Type:      "seat_update",
		EventID:   eventID,
		SeatID:    seatID,
		Status:    status,
		Timestamp: time.Now(),
	}

	return nil // Return immediately — don't wait for PG
}

// flushLoop processes buffered writes in batches.
func (wb *WriteBehindBuffer) flushLoop() {
	defer wb.wg.Done()

	batch := make([]WriteOp, 0, 100)
	ticker := time.NewTicker(2 * time.Second) // Flush every 2 seconds
	defer ticker.Stop()

	for {
		select {
		case op, ok := <-wb.buffer:
			if !ok {
				// Channel closed — flush remaining
				if len(batch) > 0 {
					wb.flushBatch(batch)
				}
				return
			}
			batch = append(batch, op)
			// Flush if batch is full
			if len(batch) >= 100 {
				wb.flushBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Flush on timer (even if batch isn't full)
			if len(batch) > 0 {
				wb.flushBatch(batch)
				batch = batch[:0]
			}

		case <-wb.ctx.Done():
			// Flush remaining on shutdown
			if len(batch) > 0 {
				wb.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch writes a batch of operations to PostgreSQL.
func (wb *WriteBehindBuffer) flushBatch(batch []WriteOp) {
	tx, err := wb.postgres.DB.Begin()
	if err != nil {
		log.Printf("[Write-Behind] ERROR: Failed to begin transaction: %v", err)
		return
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE seats SET status = $1, updated_at = $2
		WHERE event_id = $3 AND seat_id = $4`)
	if err != nil {
		log.Printf("[Write-Behind] ERROR: Failed to prepare statement: %v", err)
		return
	}
	defer stmt.Close()

	for _, op := range batch {
		switch op.Type {
		case "seat_update":
			_, execErr := stmt.Exec(op.Status, op.Timestamp, op.EventID, op.SeatID)
			if execErr != nil {
				log.Printf("[Write-Behind] ERROR: Failed to flush %s/%s: %v",
					op.EventID, op.SeatID, execErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[Write-Behind] ERROR: Batch commit failed: %v", err)
		return
	}

	log.Printf("[Write-Behind] Flushed %d operations to PostgreSQL", len(batch))
}

// Close gracefully shuts down the buffer and flushes remaining writes.
func (wb *WriteBehindBuffer) Close() {
	wb.cancel()
	close(wb.buffer)
	wb.wg.Wait()
}

// ReserveSeatWriteBehind demonstrates Write-Behind using Redis Streams.
func (s *ReservationService) ReserveSeatWriteBehind(eventID, seatID, userID string) error {
	seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)

	// Step 1: Atomic update in Redis
	err := s.rdb.HSet(s.ctx, seatsKey, seatID, "pending").Err()
	if err != nil {
		return err
	}

	// Step 2: Publish to Redis Stream (durable queue)
	s.rdb.XAdd(s.ctx, &redis.XAddArgs{
		Stream: "write_behind:seat_updates",
		Values: map[string]interface{}{
			"event_id":  eventID,
			"seat_id":   seatID,
			"status":    "pending",
			"user_id":   userID,
			"timestamp": time.Now().Unix(),
		},
	})

	log.Printf("[Write-Behind] Seat %s reserved in Redis, queued to stream", seatID)
	return nil // Return fast!
}

// WriteBehindConsumer reads from Redis Stream and writes to PostgreSQL.
// This is a background consumer that runs continuously.
func (s *ReservationService) WriteBehindConsumer() {
	if s.postgres == nil {
		log.Printf("[Write-Behind Consumer] No PostgreSQL configured, exiting")
		return
	}

	lastID := "0"
	for {
		// Block-read from stream
		results, err := s.rdb.XRead(s.ctx, &redis.XReadArgs{
			Streams: []string{"write_behind:seat_updates", lastID},
			Count:   50,
			Block:   5 * time.Second,
		}).Result()
		if err != nil {
			continue
		}

		for _, stream := range results {
			for _, msg := range stream.Messages {
				eventID, _ := msg.Values["event_id"].(string)
				seatID, _ := msg.Values["seat_id"].(string)
				status, _ := msg.Values["status"].(string)

				// Write to PostgreSQL
				_, execErr := s.postgres.DB.Exec(`
					UPDATE seats SET status = $1, updated_at = NOW()
					WHERE event_id = $2 AND seat_id = $3`,
					status, eventID, seatID,
				)
				if execErr != nil {
					log.Printf("[Write-Behind Consumer] ERROR: %v", execErr)
				} else {
					log.Printf("[Write-Behind Consumer] Flushed %s/%s = %s to PostgreSQL",
						eventID, seatID, status)
				}

				lastID = msg.ID
			}
		}
	}
}

// ============================================================================
// Pattern 6: Write-Around
// ============================================================================

// CreateEventWriteAround demonstrates the Write-Around pattern.
// Write ONLY to PostgreSQL, skip Redis cache entirely.
func (s *ReservationService) CreateEventWriteAround(event *models.Event) error {
	if s.postgres == nil {
		return fmt.Errorf("PostgreSQL not configured — Write-Around requires a database")
	}

	// Write ONLY to PostgreSQL
	err := s.postgres.InsertEvent(event)
	if err != nil {
		return err
	}
	log.Printf("[Write-Around] Event %s written to PostgreSQL only", event.ID)

	// Optionally invalidate any stale cache
	eventKey := fmt.Sprintf(eventKeyPattern, event.ID)
	s.rdb.Del(s.ctx, eventKey)

	// Redis will be populated on the FIRST READ (via Cache-Aside)
	return nil
}

// BulkImportEvents demonstrates Write-Around for bulk imports.
// Writes directly to PostgreSQL without caching — most imported data may never be read.
func (s *ReservationService) BulkImportEvents(events []*models.Event) error {
	if s.postgres == nil {
		return fmt.Errorf("PostgreSQL not configured — BulkImport requires a database")
	}

	tx, err := s.postgres.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO events (id, name, venue, event_date, total_seats, rows, seats_per_row, price_per_seat, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, event := range events {
		_, err = stmt.Exec(
			event.ID, event.Name, event.Venue, event.Date,
			event.TotalSeats, event.Rows, event.SeatsPerRow, event.PricePerSeat, event.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert event %s: %w", event.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Printf("[Write-Around] Bulk imported %d events to PostgreSQL", len(events))
	// Don't cache! Most of these may never be read
	return nil
}
