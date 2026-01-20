# Part 7: PostgreSQL Integration Deep Dive

## Overview

This part covers detailed patterns for integrating Redis Cluster with PostgreSQL, including synchronization strategies, schema design, and production best practices.

---

## 1. Integration Architecture Patterns

### Pattern 1: Write-Through (Immediate Consistency)

Write to both Redis and PostgreSQL synchronously.

```go
// Write to both stores synchronously
func (s *Service) CreateEvent(ctx context.Context, event Event) error {
    // 1. Write to PostgreSQL first (source of truth)
    err := s.insertEventPostgres(ctx, event)
    if err != nil {
        return err
    }

    // 2. Write to Redis (cache)
    err = s.cacheEventRedis(ctx, event)
    if err != nil {
        // Log warning but don't fail
        // Redis will be populated on next read
        log.Warn("Failed to cache event in Redis", err)
    }

    return nil
}
```

**Pros:**
- Strong consistency
- Simple mental model

**Cons:**
- Higher latency (two writes)
- Partial failure handling needed

### Pattern 2: Event-Driven (Eventual Consistency)

Use message queue for asynchronous synchronization.

```go
// Update Redis immediately, PostgreSQL async
func (s *Service) ConfirmReservation(ctx context.Context, resID string) error {
    // 1. Update Redis immediately (real-time state)
    err := s.markSeatsSoldInRedis(ctx, resID)
    if err != nil {
        return err
    }

    // 2. Publish event for async PostgreSQL update
    event := ReservationConfirmedEvent{
        ReservationID: resID,
        Timestamp:     time.Now(),
    }
    return s.messageQueue.Publish("reservation.confirmed", event)
}

// Background worker processes events
func (w *Worker) HandleReservationConfirmed(event ReservationConfirmedEvent) {
    w.postgres.Exec(`
        UPDATE reservations SET status = 'confirmed' WHERE id = $1
    `, event.ReservationID)
}
```

**Pros:**
- Lower latency for primary operation
- Better fault isolation

**Cons:**
- Eventual consistency (brief inconsistency window)
- More complex infrastructure

### Pattern 3: Periodic Reconciliation

Scheduled job to sync Redis with PostgreSQL.

```go
// Cron job to ensure consistency
func (s *Service) ReconcileReservations(ctx context.Context) error {
    // 1. Get confirmed reservations from PostgreSQL
    rows, _ := s.postgres.Query(`
        SELECT event_id, seat_id FROM confirmed_seats
        WHERE updated_at > $1
    `, lastSyncTime)

    // 2. Update Redis to match
    for rows.Next() {
        var eventID, seatID string
        rows.Scan(&eventID, &seatID)

        s.redis.Redis().HSet(ctx,
            fmt.Sprintf("{event:%s}:seats", eventID),
            seatID, "sold",
        )
    }

    return nil
}
```

**Pros:**
- Catches any sync failures
- Simple implementation

**Cons:**
- Delayed consistency
- Batch processing overhead

---

## 2. Complete Implementation Pattern

### The Reservation Flow with Both Systems

```go
// Reserve seats - Redis for real-time, PostgreSQL for persistence
func (s *ReservationService) ReserveSeats(ctx context.Context, req ReserveRequest) (*Reservation, error) {

    // Step 1: Atomic seat lock in Redis (fast, distributed)
    reservation, err := s.lockSeatsInRedis(ctx, req)
    if err != nil {
        return nil, err // Seats not available - fail fast
    }

    // Step 2: Record pending reservation in PostgreSQL (durable)
    err = s.recordPendingInPostgres(ctx, reservation)
    if err != nil {
        // Rollback Redis lock
        s.releaseSeatsInRedis(ctx, reservation)
        return nil, err
    }

    // Step 3: Set TTL for auto-expiration
    s.redis.Redis().Expire(ctx, reservationKey(reservation.ID), 15*time.Minute)

    return reservation, nil
}
```

### Confirmation Flow

```go
// Confirm reservation - Update both stores
func (s *ReservationService) ConfirmReservation(ctx context.Context, resID string) error {

    // Step 1: Begin PostgreSQL transaction
    tx, err := s.postgres.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Step 2: Update PostgreSQL (source of truth)
    _, err = tx.ExecContext(ctx, `
        UPDATE reservations
        SET status = 'confirmed', confirmed_at = NOW()
        WHERE id = $1 AND status = 'pending'
    `, resID)
    if err != nil {
        return err
    }

    // Step 3: Update Redis (mark seats as sold)
    err = s.markSeatsSoldInRedis(ctx, resID)
    if err != nil {
        return err // Don't commit PG if Redis fails
    }

    // Step 4: Commit PostgreSQL
    return tx.Commit()
}
```

---

## 3. PostgreSQL Schema Design

### Core Tables

```sql
-- Events table (source of truth for event metadata)
CREATE TABLE events (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    venue VARCHAR(255),
    event_date TIMESTAMP,
    total_seats INTEGER,
    price_per_seat DECIMAL(10,2),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Seats table (seat inventory)
CREATE TABLE seats (
    id VARCHAR(36) PRIMARY KEY,
    event_id VARCHAR(36) REFERENCES events(id),
    seat_code VARCHAR(10),  -- A1, A2, B1, etc.
    status VARCHAR(20) DEFAULT 'available',
    UNIQUE(event_id, seat_code)
);

-- Reservations table (booking records)
CREATE TABLE reservations (
    id VARCHAR(36) PRIMARY KEY,
    event_id VARCHAR(36) REFERENCES events(id),
    user_id VARCHAR(36),
    status VARCHAR(20) DEFAULT 'pending',
    total_amount DECIMAL(10,2),
    created_at TIMESTAMP DEFAULT NOW(),
    confirmed_at TIMESTAMP,
    cancelled_at TIMESTAMP
);

-- Reservation seats (many-to-many)
CREATE TABLE reservation_seats (
    reservation_id VARCHAR(36) REFERENCES reservations(id),
    seat_id VARCHAR(36) REFERENCES seats(id),
    PRIMARY KEY (reservation_id, seat_id)
);
```

### Indexes for Performance

```sql
-- Frequent query patterns
CREATE INDEX idx_seats_event_status ON seats(event_id, status);
CREATE INDEX idx_reservations_event ON reservations(event_id);
CREATE INDEX idx_reservations_user ON reservations(user_id);
CREATE INDEX idx_reservations_status ON reservations(status);
CREATE INDEX idx_reservations_created ON reservations(created_at);
```

---

## 4. Complete Reservation Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│              COMPLETE RESERVATION FLOW                           │
└─────────────────────────────────────────────────────────────────┘

User: "Reserve seats A1, A2 for Concert"
                    │
                    ▼
┌──────────────────────────────────────┐
│ 1. CHECK AVAILABILITY (Redis)        │
│    HGET {event:123}:seats A1         │
│    HGET {event:123}:seats A2         │
│    → Both "available"                │
└──────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────┐
│ 2. ATOMIC LOCK (Redis Lua Script)    │
│    - Check seats still available     │
│    - Mark as "pending:res456"        │
│    - Set 15-min TTL                  │
└──────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────┐
│ 3. RECORD PENDING (PostgreSQL)       │
│    INSERT INTO reservations          │
│    INSERT INTO reservation_seats     │
│    → Transaction committed           │
└──────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────┐
│ 4. RETURN TO USER                    │
│    "Reservation res456 created"      │
│    "Complete payment within 15 min"  │
└──────────────────────────────────────┘
                    │
        ┌───────────┴───────────┐
        ▼                       ▼
┌───────────────┐       ┌───────────────┐
│ User Pays     │       │ User Abandons │
│               │       │ (15 min TTL)  │
└───────┬───────┘       └───────┬───────┘
        │                       │
        ▼                       ▼
┌───────────────┐       ┌───────────────┐
│ 5a. CONFIRM   │       │ 5b. EXPIRE    │
│               │       │               │
│ Redis:        │       │ Redis:        │
│ Mark "sold"   │       │ TTL expires   │
│               │       │ Key deleted   │
│ PostgreSQL:   │       │               │
│ status =      │       │ PostgreSQL:   │
│ 'confirmed'   │       │ status =      │
│               │       │ 'expired'     │
└───────────────┘       └───────────────┘
                               │
                               ▼
                        ┌───────────────┐
                        │ NOTIFY        │
                        │ WAITLIST      │
                        │ (if any)      │
                        └───────────────┘
```

---

## 5. Failure Handling Strategies

### Redis Unavailable

```go
func (s *Service) GetSeatAvailability(ctx context.Context, eventID string) ([]Seat, error) {
    // Try Redis first
    seats, err := s.redis.GetSeats(ctx, eventID)
    if err == nil {
        return seats, nil
    }

    // Fallback to PostgreSQL
    log.Warn("Redis unavailable, falling back to PostgreSQL")
    return s.postgres.GetSeats(ctx, eventID)
}
```

### PostgreSQL Unavailable

```go
func (s *Service) ConfirmReservation(ctx context.Context, resID string) error {
    // Try PostgreSQL
    err := s.postgres.Confirm(ctx, resID)
    if err != nil {
        // Queue for retry
        s.retryQueue.Add(ConfirmJob{ResID: resID})
        return fmt.Errorf("confirmation queued for retry")
    }

    // Update Redis
    return s.redis.MarkSold(ctx, resID)
}
```

### Both Unavailable

```go
func (s *Service) HandleDoubleFailure() error {
    return errors.New("service temporarily unavailable")
}
```

---

## 6. Best Practices Summary

### 1. Define Clear Ownership

```
Redis Cluster:
├── Real-time state
├── Sessions
├── Counters
└── Distributed locks

PostgreSQL:
├── Historical data
├── Transactions
└── Reports
```

### 2. Handle Failures Gracefully

```
Redis down?
└── Fall back to PostgreSQL (slower but works)

PostgreSQL down?
└── Queue writes, serve from Redis cache

Both down?
└── Return service unavailable
```

### 3. Use Appropriate TTLs

```
Pending reservations: 15 minutes
Event cache: 1 hour (or invalidate on update)
Session data: Match session timeout
```

### 4. Implement Idempotency

```go
// Use unique reservation IDs
// Check before insert in PostgreSQL
// Handle duplicate Redis writes gracefully
```

### 5. Monitor Consistency

```
Track sync lag metrics
Alert on reconciliation mismatches
Run periodic consistency checks
```

### 6. Transaction Boundaries

```
1. Start with Redis (fast fail for unavailable seats)
2. Commit to PostgreSQL (durable)
3. Confirm in Redis (or rollback both)
```

---

## 7. Monitoring Recommendations

### Redis Metrics to Watch

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `connected_clients` | Active connections | > 80% of max |
| `used_memory` | Memory usage | > 80% of maxmemory |
| `keyspace_hits/misses` | Cache hit ratio | < 80% hit rate |
| `instantaneous_ops_per_sec` | Throughput | Baseline deviation |

### PostgreSQL Metrics to Watch

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `active_connections` | Active queries | > 80% of max |
| `replication_lag` | Replica delay | > 5 seconds |
| `cache_hit_ratio` | Buffer cache | < 95% |
| `transaction_duration` | Long transactions | > 30 seconds |

### Application Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `redis_latency_p99` | 99th percentile | > 50ms |
| `postgres_latency_p99` | 99th percentile | > 200ms |
| `sync_lag_seconds` | Redis-PG sync delay | > 60 seconds |
| `reservation_success_rate` | Business metric | < 99% |

---

## 8. Final Summary

### Key Takeaways

| Concept | Redis Cluster Role | PostgreSQL Role |
|---------|-------------------|-----------------|
| **Sharding** | Automatic via 16384 hash slots | Table partitioning if needed |
| **High Availability** | Master-replica failover | Streaming replication |
| **Real-time Data** | Primary store | Eventual sync |
| **Durable Storage** | TTL-based expiration | Primary store |
| **Complex Queries** | Limited (key-value) | Full SQL support |

### Remember

1. **Redis Cluster** for real-time, distributed state
2. **PostgreSQL** for durable, queryable history
3. Use **hash tags** for related data co-location
4. **Lua scripts** for atomic multi-key operations
5. **Design for failure** - both systems should handle partner unavailability
6. **Monitor both systems** and their synchronization
7. **Test failure scenarios** before production

### Architecture Decision Guide

```
Choose Redis when:
├── Millisecond latency required
├── Data can be reconstructed
├── TTL-based expiration needed
└── Distributed locking required

Choose PostgreSQL when:
├── ACID transactions required
├── Complex queries needed
├── Long-term storage
└── Regulatory compliance required

Use Both when:
├── Real-time + durability needed
├── High read throughput + complex queries
└── Session management + user history
```
