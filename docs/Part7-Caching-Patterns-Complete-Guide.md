# Part 7: Complete Caching Patterns — Hands-On Lab Guide

## Overview

In this lab you will **build, run, and observe** all 6 caching patterns used when integrating Redis with PostgreSQL. Each pattern starts with a fundamental explanation, then walks you through a step-by-step lab exercise using our ticket reservation system.

**What you will learn:**

| # | Pattern | Category | Key Idea |
|---|---------|----------|----------|
| 1 | **Cache-Aside** (Lazy Loading) | Read | App checks cache, loads from DB on miss |
| 2 | **Read-Through** | Read | Cache auto-loads from DB on miss |
| 3 | **Refresh-Ahead** | Read | Background refresh before TTL expires |
| 4 | **Write-Through** | Write | Sync write to both cache and DB |
| 5 | **Write-Behind** (Write-Back) | Write | Write to cache now, DB later (async) |
| 6 | **Write-Around** | Write | Write to DB only, skip cache |

```
┌──────────────────────────────────────────────────────────────────┐
│                    THE 6 CACHING PATTERNS                        │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  READ PATTERNS              WRITE PATTERNS                       │
│  ─────────────              ──────────────                       │
│  1. Cache-Aside             4. Write-Through                     │
│  2. Read-Through            5. Write-Behind (Write-Back)         │
│  3. Refresh-Ahead           6. Write-Around                      │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

**Prerequisites:**
- Completed Labs 1–6 (Redis Cluster running)
- Docker and Docker Compose installed
- Go 1.21+ installed
- `psql` CLI (optional, for manual verification)

**Duration:** ~60 minutes

---

## Section 1: Environment Setup

### Step 1.1: Start Docker Services

Before running any caching pattern, you need both **Redis Cluster** and **PostgreSQL** running.

```bash
# Navigate to the project root
cd /path/to/redis-cluster-lab

# Start all services (Redis Cluster + PostgreSQL)
docker-compose up -d
```

### Step 1.2: Verify Redis Cluster

```bash
# Check Redis cluster is healthy
docker exec redis-1 redis-cli -p 7001 cluster info | grep cluster_state
# Expected: cluster_state:ok

# Check cluster nodes
docker exec redis-1 redis-cli -p 7001 cluster nodes | head -6
```

### Step 1.3: Verify PostgreSQL

```bash
# Check PostgreSQL is ready
docker exec postgres pg_isready -U postgres
# Expected: /var/run/postgresql:5432 - accepting connections

# Connect and verify database exists
docker exec postgres psql -U postgres -d ticket_reservation -c "SELECT 1;"
# Expected:  ?column?
#            ----------
#                    1
```

> **Note:** PostgreSQL runs on container port `5432` internally, but is mapped to **host port `5533`** to avoid conflicts with any local PostgreSQL.

### Step 1.4: Set Environment Variables

```bash
# PostgreSQL connection string (host port 5533)
export PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable"

# Verify connection from host
psql "$PG_DSN" -c "SELECT current_database();"
# Expected: ticket_reservation
```

### Step 1.5: Build the Application

```bash
cd app
go build -o ticket-reservation .

# Verify the build
./ticket-reservation help
```

### Step 1.6: Initialize Test Data

```bash
# Create a test event with PostgreSQL integration
./ticket-reservation create-event --name "Caching Patterns Lab" --rows 5 --seats 10 --price 100

# Note the event ID from the output (e.g., "a1b2c3d4")
# Save it for use in all exercises below:
export EVENT_ID="<paste-event-id-here>"
```

---

## Section 2: Fundamentals — How Caching Works

### What is a Cache?

A **cache** is a fast, temporary data store placed between the application and the slower primary database. The goal is to reduce latency and database load.

```
WITHOUT CACHE:                    WITH CACHE:

App ──► PostgreSQL ──► App        App ──► Redis (fast!) ──► App
         ~5-50ms                           ~0.5ms

                                  Only on MISS:
                                  App ──► PostgreSQL ──► Redis ──► App
```

### Key Terminology

| Term | Definition |
|------|-----------|
| **Cache Hit** | Data found in cache — fast return |
| **Cache Miss** | Data not in cache — must load from DB |
| **TTL** | Time-To-Live — how long cached data stays before expiring |
| **Invalidation** | Removing stale data from cache |
| **Warm Cache** | Cache that has been pre-loaded with data |
| **Cold Cache** | Empty cache — all reads will miss |
| **Eviction** | Removing data to free memory (LRU, LFU, etc.) |

### Redis Data Types for Caching

```
┌──────────────────────────────────────────────────────┐
│  Redis Type    │  Cache Use Case                      │
├──────────────────────────────────────────────────────┤
│  STRING        │  JSON objects (event, reservation)   │
│  HASH          │  Structured records (seat statuses)  │
│  SORTED SET    │  Rankings, leaderboards, waitlists   │
│  SET           │  Unique collections, membership      │
│  LIST          │  Queues, recent items                 │
└──────────────────────────────────────────────────────┘
```

### Hash Tags in Redis Cluster

Our app uses **hash tags** to ensure related keys land on the same node:

```
{event:abc123}           ─┐
{event:abc123}:seats      │  Same slot → same node
{event:abc123}:stats      │  Enables Lua scripts across keys
{event:abc123}:waitlist  ─┘
```

---

## Section 3: Pattern 1 — Cache-Aside (Lazy Loading)

### 3.1 Concept

The **application** manages the cache directly. On a read:
1. Check Redis — if **hit**, return cached data
2. If **miss**, query PostgreSQL
3. **Store the result in Redis** for future reads
4. Return data to caller

The cache is populated **lazily** — only when data is actually requested.

```
        ┌──────────┐
        │   App    │
        └────┬─────┘
             │
     ┌───────┴───────┐
     │  1. GET key   │
     ▼               │
┌─────────┐          │
│  Redis  │          │
│ (Cache) │          │
└────┬────┘          │
     │               │
  HIT? ──── YES ───► Return cached data
     │
     NO
     │
     ▼
┌──────────┐
│PostgreSQL│  2. SELECT ... FROM events
│  (DB)    │
└────┬─────┘
     │
     ▼
  3. SET key value EX ttl   ◄── Store in Redis
     │
     ▼
  4. Return data to caller
```

### 3.2 Code Example

```go
// Cache-Aside pattern for GetEvent
// The APPLICATION is responsible for managing the cache
func (s *ReservationService) GetEventCacheAside(eventID string) (*models.Event, error) {
    eventKey := fmt.Sprintf("{event:%s}", eventID)

    // Step 1: Check Redis cache
    eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
    if err == nil {
        // CACHE HIT — deserialize and return
        var event models.Event
        json.Unmarshal([]byte(eventJSON), &event)
        log.Printf("[Cache-Aside] HIT for event %s", eventID)
        return &event, nil
    }

    // Step 2: CACHE MISS — query PostgreSQL
    log.Printf("[Cache-Aside] MISS for event %s — loading from PostgreSQL", eventID)
    event, err := s.postgres.GetEvent(eventID)
    if err != nil {
        return nil, fmt.Errorf("event not found: %w", err)
    }

    // Step 3: Populate cache for future reads (with TTL)
    data, _ := json.Marshal(event)
    s.rdb.Set(s.ctx, eventKey, data, 1*time.Hour)
    log.Printf("[Cache-Aside] Cached event %s (TTL: 1 hour)", eventID)

    // Step 4: Return data
    return event, nil
}
```

### 3.3 Cache-Aside with Hash Type

```go
// Cache-Aside with Hash type — seat statuses
func (s *ReservationService) GetSeatsCacheAside(eventID string) (map[string]string, error) {
    seatsKey := fmt.Sprintf("{event:%s}:seats", eventID)

    // Step 1: Check Redis
    seatsMap, err := s.rdb.HGetAll(s.ctx, seatsKey).Result()
    if err == nil && len(seatsMap) > 0 {
        log.Printf("[Cache-Aside] HIT — %d seats from Redis", len(seatsMap))
        return seatsMap, nil
    }

    // Step 2: Cache miss — load from PostgreSQL
    log.Printf("[Cache-Aside] MISS — loading seats from PostgreSQL")
    seats, err := s.postgres.GetSeats(eventID)
    if err != nil {
        return nil, err
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
```

### 3.4 Cache Invalidation with Cache-Aside

```go
// When data changes, INVALIDATE the cache (don't update it)
func (s *ReservationService) UpdateEventCacheAside(eventID string, name string) error {
    // Step 1: Update PostgreSQL (source of truth)
    _, err := s.postgres.DB.Exec(
        `UPDATE events SET name = $1 WHERE id = $2`, name, eventID,
    )
    if err != nil {
        return err
    }

    // Step 2: DELETE from cache (next read will re-populate)
    eventKey := fmt.Sprintf("{event:%s}", eventID)
    s.rdb.Del(s.ctx, eventKey)
    log.Printf("[Cache-Aside] Invalidated cache for event %s", eventID)

    return nil
}
```

### 3.5 Hands-On Lab — Cache-Aside

**Goal:** Observe cache miss → PostgreSQL load → cache hit behavior.

#### Exercise 1: Observe Cache Miss and Hit

```bash
# Step 1: Check if event exists in Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$EVENT_ID}"
# Expected: event JSON (because create-event wrote it)

# Step 2: Delete the Redis key to simulate a cold cache
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}"
# Expected: (integer) 1

# Step 3: Now read the event — this triggers Cache-Aside
./ticket-reservation availability $EVENT_ID
# Look at the log output — you should see:
#   [Fallback] Redis unavailable for stats ..., falling back to PostgreSQL

# Step 4: Check Redis again — the fallback populated the cache
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$EVENT_ID}"
# Expected: event JSON is back (loaded from PostgreSQL)
```

#### Exercise 2: Compare Latency

```bash
# Warm cache read (Redis hit)
time ./ticket-reservation availability $EVENT_ID
# Expected: very fast (~10-50ms including process startup)

# Cold cache read (delete key first, then read)
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}:stats"
time ./ticket-reservation availability $EVENT_ID
# Expected: slightly slower (PostgreSQL fallback)
```

#### Exercise 3: Verify in PostgreSQL

```bash
# Verify event exists in PostgreSQL (source of truth)
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, venue, total_seats FROM events WHERE id = '$EVENT_ID';"
```

### 3.6 Pros & Cons

| Pros | Cons |
|------|------|
| Only caches data that is actually used | First request is always slow (cache miss) |
| Simple to implement | Stale data possible if DB is updated directly |
| Application has full control | Application must manage cache logic |
| Easy to reason about | N+1 cache miss problem on cold start |

### 3.7 When to Use

- Read-heavy workloads with infrequent updates
- Data that is expensive to compute (aggregations, reports)
- When you can tolerate brief staleness
- **Our app**: `GetEvent` fallback pattern is essentially Cache-Aside

### 3.8 Tips: Refactoring to Clean Code

The Cache-Aside pattern often leads to **repetitive if-else blocks** mixed into business logic. A cleaner approach is to separate data-fetching into small, focused functions and compose them using a **"try first, fallback second"** helper.

#### Problem: Tangled Logic

```go
// BAD — cache logic, deserialization, DB access, and caching are all mixed together
func (s *ReservationService) GetEventCacheAside(eventID string) (*models.Event, error) {
    eventKey := fmt.Sprintf("{event:%s}", eventID)
    eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
    if err == nil {
        var event models.Event
        json.Unmarshal([]byte(eventJSON), &event)
        return &event, nil
    }
    event, err := s.postgres.GetEvent(eventID)
    if err != nil {
        return nil, err
    }
    data, _ := json.Marshal(event)
    s.rdb.Set(s.ctx, eventKey, data, 1*time.Hour)
    return event, nil
}
```

This works, but as you add more entities (seats, reservations, stats), you copy-paste the same cache-check → fallback → populate pattern everywhere.

#### Solution: Separate Fetch Functions + Generic Fallback

**Step 1:** Create small, single-purpose fetch functions that return `func() (T, error)`:

```go
// redisGetEvent returns a function that fetches an event from Redis
func (s *ReservationService) redisGetEvent(eventID string) func() (*models.Event, error) {
    return func() (*models.Event, error) {
        eventKey := fmt.Sprintf("{event:%s}", eventID)
        eventJSON, err := s.rdb.Get(s.ctx, eventKey).Result()
        if err != nil {
            return nil, err
        }
        var event models.Event
        if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
            return nil, err
        }
        return &event, nil
    }
}

// pgGetEvent returns a function that fetches an event from PostgreSQL
func (s *ReservationService) pgGetEvent(eventID string) func() (*models.Event, error) {
    return func() (*models.Event, error) {
        return s.postgres.GetEvent(eventID)
    }
}
```

**Step 2:** Write a generic fallback helper:

```go
// withFallback tries the primary function first; if it fails, calls fallback.
func withFallback[T any](primary, fallback func() (T, error)) (T, error) {
    result, err := primary()
    if err == nil {
        return result, nil
    }
    return fallback()
}
```

**Step 3:** Compose them cleanly:

```go
// CLEAN — business logic reads like a sentence
func (s *ReservationService) GetEventClean(eventID string) (*models.Event, error) {
    return withFallback(
        s.redisGetEvent(eventID),    // try Redis first
        s.pgGetEvent(eventID),       // fallback to PostgreSQL
    )
}
```

#### Full Example with Cache Population

Add a wrapper that populates the cache after a fallback:

```go
// withCacheAside tries Redis, falls back to PG, and populates Redis on miss.
func (s *ReservationService) withCacheAside[T any](
    cacheKey string,
    ttl time.Duration,
    fromCache func() (T, error),
    fromDB func() (T, error),
) (T, error) {
    // Step 1: Try cache
    result, err := fromCache()
    if err == nil {
        log.Printf("[Cache-Aside] HIT %s", cacheKey)
        return result, nil
    }

    // Step 2: Fallback to DB
    log.Printf("[Cache-Aside] MISS %s", cacheKey)
    result, err = fromDB()
    if err != nil {
        return result, err
    }

    // Step 3: Populate cache
    data, _ := json.Marshal(result)
    s.rdb.Set(s.ctx, cacheKey, data, ttl)
    log.Printf("[Cache-Aside] Cached %s (TTL: %v)", cacheKey, ttl)

    return result, nil
}
```

Now every Cache-Aside call is a one-liner:

```go
func (s *ReservationService) GetEventClean(eventID string) (*models.Event, error) {
    return s.withCacheAside(
        fmt.Sprintf("{event:%s}", eventID),
        1*time.Hour,
        s.redisGetEvent(eventID),
        s.pgGetEvent(eventID),
    )
}

func (s *ReservationService) GetReservationClean(resID string) (*models.Reservation, error) {
    return s.withCacheAside(
        fmt.Sprintf("reservation:%s", resID),
        15*time.Minute,
        s.redisGetReservation(resID),
        s.pgGetReservation(resID),
    )
}
```

#### Before vs After

```
BEFORE (repeated pattern):                AFTER (clean composition):

GetEvent:                                 GetEvent:
  if redis.Get() == ok → return             return withCacheAside(
  pg.GetEvent()                               redisGetEvent(),
  redis.Set()                                 pgGetEvent(),
  return                                    )

GetReservation:                           GetReservation:
  if redis.Get() == ok → return             return withCacheAside(
  pg.GetReservation()                         redisGetReservation(),
  redis.Set()                                 pgGetReservation(),
  return                                    )

GetSeats:                                 GetSeats:
  if redis.HGetAll() == ok → return         return withCacheAside(
  pg.GetSeats()                               redisGetSeats(),
  redis.HSet()                                pgGetSeats(),
  return                                    )

  ↑ Copy-paste everywhere                   ↑ One pattern, many uses
```

#### Benefits

| Aspect | Before | After |
|--------|--------|-------|
| Lines per entity | ~15–20 | ~5 |
| Cache logic | Duplicated in each function | Single `withCacheAside` helper |
| Testing | Must test cache + DB + populate in every function | Test `withCacheAside` once, mock fetch functions |
| Adding new entity | Copy-paste cache boilerplate | Add two fetch functions + one call |
| Readability | Must trace if-else branches | Reads like: "try cache, fallback DB" |

> **Note:** Go 1.18+ generics (`withFallback[T any]`, `withCacheAside[T any]`) make this pattern type-safe. For Go < 1.18, use `interface{}` with type assertions.

---

## Section 4: Pattern 2 — Read-Through

### 4.1 Concept

The **cache itself** is responsible for loading data from the database on a miss. The application only talks to the cache — never directly to the DB for reads.

```
        ┌──────────┐
        │   App    │
        └────┬─────┘
             │
     1. GET key
             │
             ▼
     ┌───────────────┐
     │    Redis      │
     │  (Smart Cache)│
     │               │
     │  HIT? Return  │
     │               │
     │  MISS?        │
     │   ┌───────┐   │       ┌──────────┐
     │   │ Load  │───┼──────►│PostgreSQL│
     │   │ from  │◄──┼──────┤│  (DB)    │
     │   │  DB   │   │       └──────────┘
     │   └───┬───┘   │
     │       │       │
     │  Cache it     │
     │       │       │
     └───────┼───────┘
             │
     Return data
             │
             ▼
        ┌──────────┐
        │   App    │
        └──────────┘
```

### 4.2 Key Difference from Cache-Aside

```
┌─────────────────────────────────────────────────────────────┐
│                    CACHE-ASIDE                               │
│                                                              │
│   App ──► Redis (miss?) ──► App ──► PostgreSQL ──► App       │
│                                         │                    │
│                                    App ──► Redis (SET)       │
│                                                              │
│   The APP manages the cache manually                         │
├─────────────────────────────────────────────────────────────┤
│                    READ-THROUGH                              │
│                                                              │
│   App ──► Cache (miss?) ──► Cache ──► PostgreSQL             │
│                                  │                           │
│                             Cache stores automatically       │
│                                  │                           │
│                             Cache ──► App                    │
│                                                              │
│   The CACHE manages loading transparently                    │
└─────────────────────────────────────────────────────────────┘
```

| Aspect | Cache-Aside | Read-Through |
|--------|-------------|--------------|
| Who loads on miss? | Application | Cache layer |
| Application complexity | Higher | Lower |
| DB coupling | App knows about DB | Cache knows about DB |
| Reusability | Logic in each caller | One cache layer, many callers |

### 4.3 Code Example — Read-Through Cache Wrapper

```go
// ReadThroughCache wraps Redis with automatic PostgreSQL loading
type ReadThroughCache struct {
    rdb      *redis.ClusterClient
    postgres *db.PostgresDB
    ctx      context.Context
}

// Get implements the Read-Through pattern
// The caller never touches PostgreSQL directly
func (c *ReadThroughCache) GetEvent(eventID string) (*models.Event, error) {
    eventKey := fmt.Sprintf("{event:%s}", eventID)

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
    json.Unmarshal([]byte(eventJSON), &event)
    return &event, nil
}

// loadAndCache is INTERNAL to the cache — caller doesn't know about it
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
```

### 4.4 Read-Through for Computed Statistics

```go
// Read-Through for expensive computed data
func (c *ReadThroughCache) GetEventStats(eventID string) (*models.EventStats, error) {
    cacheKey := fmt.Sprintf("{event:%s}:computed_stats", eventID)

    // Try cache first
    data, err := c.rdb.Get(c.ctx, cacheKey).Result()
    if err == nil {
        var stats models.EventStats
        json.Unmarshal([]byte(data), &stats)
        return &stats, nil
    }

    // Auto-load: compute from PostgreSQL (expensive query)
    stats, err := c.postgres.GetEventStats(eventID)
    if err != nil {
        return nil, err
    }

    // Cache computed result (shorter TTL for dynamic data)
    jsonData, _ := json.Marshal(stats)
    c.rdb.Set(c.ctx, cacheKey, jsonData, 5*time.Minute)
    log.Printf("[Read-Through] Cached computed stats for event %s", eventID)

    return stats, nil
}
```

### 4.5 Hands-On Lab — Read-Through

**Goal:** Understand how Read-Through differs from Cache-Aside by inspecting the code structure.

#### Exercise 1: Trace the Code Path

Look at these two files and compare:

```bash
# Cache-Aside (our actual GetEvent with Fallback):
# File: app/service/reservation.go, lines 133-160
#
# Notice: the SERVICE (caller) handles the fallback logic:
#   1. Try Redis
#   2. If fail → SERVICE calls PostgreSQL
#   3. SERVICE returns result

# Read-Through (conceptual wrapper):
# The CACHE WRAPPER handles everything:
#   1. Caller asks cache: cache.GetEvent(id)
#   2. Cache checks Redis
#   3. Cache loads from PG on miss
#   4. Cache returns result — caller never knows about PG
```

#### Exercise 2: Simulate Read-Through with redis-cli

```bash
# Step 1: Delete event from Redis (simulate cold cache)
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}"

# Step 2: Read event via the app (triggers auto-load from PG)
./ticket-reservation get-key "{event:$EVENT_ID}"
# Expected: "Key not found" (because get-key only checks Redis)

# Step 3: Now use the application command (which has fallback/read-through logic)
./ticket-reservation availability $EVENT_ID
# Expected: Stats loaded from PostgreSQL

# Step 4: Check Redis — the read automatically re-populated the cache
docker exec redis-1 redis-cli -p 7001 -c EXISTS "{event:$EVENT_ID}"
# Expected: (integer) 1 — key is back!
```

#### Exercise 3: Verify the Transparent Nature

```bash
# The caller (CLI) doesn't know whether data came from Redis or PG
# Both calls return the same result:

# Call 1 — cold cache (loads from PG)
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}:stats"
./ticket-reservation availability $EVENT_ID

# Call 2 — warm cache (served from Redis)
./ticket-reservation availability $EVENT_ID

# Both return identical output — that's the beauty of Read-Through
```

---

## Section 5: Pattern 3 — Refresh-Ahead (Predictive Refresh)

### 5.1 Concept

Proactively **refresh cache entries before they expire**. When a cached item's TTL drops below a threshold, a background process reloads it from the DB — so the next read always hits a warm cache.

```
        ┌──────────────────────────────────────┐
        │          TIMELINE                     │
        │                                       │
        │  SET (TTL=60s)                        │
        │  ├─────────────────────────────────┤  │
        │  0s              42s      48s   60s   │
        │  │                │        │     │    │
        │  │    Normal      │Refresh │     │    │
        │  │    reads       │Window  │     │    │
        │  │    (HIT)       │(70%)   │     │    │
        │  │                │        │     │    │
        │  │                ▼        │     │    │
        │  │          Background     │     │    │
        │  │          reload from    │     │    │
        │  │          PostgreSQL     │     │    │
        │  │                │        │     │    │
        │  │                ▼        │     │    │
        │  │          SET key value  │     │    │
        │  │          EX 60s (reset) │     │    │
        │  │                         │     │    │
        │  │                    Never expires!  │
        └──────────────────────────────────────┘
```

### 5.2 Code Example — Refresh-Ahead for Hot Events

```go
const (
    eventCacheTTL      = 10 * time.Minute
    refreshThreshold   = 0.7 // Refresh when 70% of TTL has elapsed
)

// GetEventRefreshAhead reads with proactive refresh
func (s *ReservationService) GetEventRefreshAhead(eventID string) (*models.Event, error) {
    eventKey := fmt.Sprintf("{event:%s}", eventID)

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
    remainingRatio := float64(ttl) / float64(eventCacheTTL)

    // Step 3: If TTL is below threshold, trigger background refresh
    if remainingRatio < (1 - refreshThreshold) {
        log.Printf("[Refresh-Ahead] TTL at %.0f%% for event %s — triggering background refresh",
            remainingRatio*100, eventID)
        go s.refreshEventCache(eventID, eventKey) // Non-blocking!
    }

    // Step 4: Return cached data immediately (always fast)
    var event models.Event
    json.Unmarshal([]byte(eventJSON), &event)
    return &event, nil
}

// refreshEventCache reloads from PostgreSQL in the background
func (s *ReservationService) refreshEventCache(eventID, cacheKey string) {
    event, err := s.postgres.GetEvent(eventID)
    if err != nil {
        log.Printf("[Refresh-Ahead] Background refresh failed for %s: %v", eventID, err)
        return
    }

    data, _ := json.Marshal(event)
    s.rdb.Set(s.ctx, cacheKey, data, eventCacheTTL)
    log.Printf("[Refresh-Ahead] Refreshed event %s (new TTL: %v)", eventID, eventCacheTTL)
}

// loadEventIntoCache handles cold cache miss
func (s *ReservationService) loadEventIntoCache(eventID, cacheKey string) (*models.Event, error) {
    event, err := s.postgres.GetEvent(eventID)
    if err != nil {
        return nil, err
    }

    data, _ := json.Marshal(event)
    s.rdb.Set(s.ctx, cacheKey, data, eventCacheTTL)
    log.Printf("[Refresh-Ahead] Cold load event %s into cache", eventID)

    return event, nil
}
```

### 5.3 Background Refresh Worker

```go
// RefreshAheadWorker continuously monitors and refreshes hot keys
type RefreshAheadWorker struct {
    rdb      *redis.ClusterClient
    postgres *db.PostgresDB
    ctx      context.Context
    hotKeys  map[string]RefreshConfig
}

type RefreshConfig struct {
    TTL       time.Duration
    Threshold float64 // 0.0 to 1.0
    Loader    func(key string) ([]byte, error)
}

// Start begins the refresh-ahead monitoring loop
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

func (w *RefreshAheadWorker) checkAndRefresh() {
    for key, config := range w.hotKeys {
        ttl, err := w.rdb.TTL(w.ctx, key).Result()
        if err != nil || ttl < 0 {
            continue
        }

        remainingRatio := float64(ttl) / float64(config.TTL)
        if remainingRatio < (1 - config.Threshold) {
            data, err := config.Loader(key)
            if err == nil {
                w.rdb.Set(w.ctx, key, data, config.TTL)
                log.Printf("[Refresh-Ahead Worker] Refreshed key %s", key)
            }
        }
    }
}
```

### 5.4 Hands-On Lab — Refresh-Ahead

**Goal:** Observe TTL behavior and understand when a refresh would trigger.

#### Exercise 1: Set a Short TTL and Watch It Decay

```bash
# Step 1: Set a key with a 30-second TTL
docker exec redis-1 redis-cli -p 7001 -c SET "demo:refresh" "original_data" EX 30
# Expected: OK

# Step 2: Watch the TTL decay
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"
# Expected: ~30

# Step 3: Wait 10 seconds, then check again
sleep 10
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"
# Expected: ~20

# Step 4: At 70% elapsed (21 seconds), Refresh-Ahead would trigger
# The remaining TTL would be ~9 seconds (30% remaining)
sleep 12
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"
# Expected: ~8 — this is when background refresh fires!
```

#### Exercise 2: Simulate Refresh-Ahead Manually

```bash
# Step 1: Set event data with a 60-second TTL
docker exec redis-1 redis-cli -p 7001 -c SET "{event:$EVENT_ID}:demo_stats" '{"total":50,"sold":10}' EX 60

# Step 2: Check TTL
docker exec redis-1 redis-cli -p 7001 -c TTL "{event:$EVENT_ID}:demo_stats"
# Expected: ~60

# Step 3: Wait for 70% of TTL to elapse (42 seconds)
sleep 42

# Step 4: Check TTL — should be in refresh window
docker exec redis-1 redis-cli -p 7001 -c TTL "{event:$EVENT_ID}:demo_stats"
# Expected: ~18 (below 30% threshold)

# Step 5: Simulate the background refresh (reset TTL with fresh data)
docker exec redis-1 redis-cli -p 7001 -c SET "{event:$EVENT_ID}:demo_stats" '{"total":50,"sold":15}' EX 60
# Now TTL is reset to 60 seconds — key never expired!

# Step 6: Verify new data
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$EVENT_ID}:demo_stats"
# Expected: {"total":50,"sold":15} — updated without any cache miss!

# Cleanup
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}:demo_stats"
```

### 5.5 Pros & Cons

| Pros | Cons |
|------|------|
| Zero cache misses for hot data | Complexity of background refresh logic |
| Consistently low latency | Unnecessary refreshes for rarely-accessed data |
| Great for predictable access patterns | Resource usage for monitoring TTLs |

---

## Section 6: Pattern 4 — Write-Through (Already Implemented)

### 6.1 Concept

Write to **both** the cache and database **synchronously** on every write. The write only succeeds when both stores are updated.

```
        ┌──────────┐
        │   App    │
        │  WRITE   │
        └────┬─────┘
             │
     ┌───────┴───────┐
     │               │
     ▼               ▼
┌─────────┐   ┌──────────┐
│  Redis  │   │PostgreSQL│
│ (Cache) │   │  (DB)    │
└─────────┘   └──────────┘
     │               │
     └───────┬───────┘
             │
     Both succeed = success
     Either fails = error
```

### 6.2 Code Example — Our Actual Implementation

**This is what our app already does.** See `app/service/reservation.go`.

```go
// Write-Through: CreateEvent (actual code in our app)
func (s *ReservationService) CreateEvent(...) (*models.Event, error) {
    // Step 1: Write to PostgreSQL FIRST (source of truth)
    if s.postgres != nil {
        if err := s.postgres.InsertEvent(event); err != nil {
            return nil, fmt.Errorf("[PG] failed: %w", err)
        }
        log.Printf("[Write-Through] Written to PostgreSQL")
    }

    // Step 2: Write to Redis (cache)
    pipe := s.rdb.Pipeline()
    pipe.Set(s.ctx, eventKey, eventJSON, 0)
    pipe.HSet(s.ctx, seatsKey, seatData)
    pipe.HSet(s.ctx, statsKey, statsMap)
    _, err = pipe.Exec(s.ctx)

    if err != nil && s.postgres != nil {
        log.Printf("[Write-Through] Redis write failed, PG has data")
        // Don't fail — PG is the source of truth
    }

    return event, nil
}
```

```go
// Write-Through: ConfirmReservation
func (s *ReservationService) ConfirmReservation(reservationID, paymentID string) (...) {
    // Step 1: Update Redis (seats → sold, via Lua script)
    confirmScript.Run(s.ctx, s.rdb, keys, args...)

    // Step 2: Update Redis reservation record
    s.rdb.Set(s.ctx, resKey, resJSON, 0)

    // Step 3: Write-Through to PostgreSQL
    if s.postgres != nil {
        s.postgres.UpdateReservationStatus(reservationID, models.ReservationConfirmed, paymentID)
        s.postgres.UpdateSeatStatuses(eventID, seats, models.SeatSold, userID)
    }
}
```

### 6.3 Hands-On Lab — Write-Through

**Goal:** Observe data being written to both Redis and PostgreSQL simultaneously.

#### Exercise 1: Create Event and Verify Both Stores

```bash
# Step 1: Create a new event
./ticket-reservation create-event --name "Write-Through Test" --rows 3 --seats 5 --price 75
# Note the event ID from output
export WT_EVENT="<event-id>"

# Step 2: Verify in Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$WT_EVENT}"
# Expected: JSON with event data

docker exec redis-1 redis-cli -p 7001 -c HGETALL "{event:$WT_EVENT}:seats"
# Expected: 15 seats, all "available"

# Step 3: Verify in PostgreSQL
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, venue, total_seats FROM events WHERE id = '$WT_EVENT';"
# Expected: Same event data

docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status FROM seats WHERE event_id = '$WT_EVENT' ORDER BY seat_id LIMIT 10;"
# Expected: All seats with status 'available'
```

#### Exercise 2: Reserve Seats and Verify Both Stores

```bash
# Step 1: Reserve seats A1, A2
./ticket-reservation reserve --event $WT_EVENT --user user1 --seats A1,A2 \
  --name "John Doe" --email "john@example.com"
# Note the reservation ID
export RES_ID="<reservation-id>"

# Step 2: Check Redis — seats should be "pending"
docker exec redis-1 redis-cli -p 7001 -c HMGET "{event:$WT_EVENT}:seats" A1 A2
# Expected: "pending" "pending"

# Step 3: Check PostgreSQL — should also show "pending"
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status, held_by FROM seats WHERE event_id = '$WT_EVENT' AND seat_id IN ('A1','A2');"
# Expected: Both pending, held_by = user1
```

#### Exercise 3: Confirm Reservation and Verify Both Stores

```bash
# Step 1: Confirm the reservation
./ticket-reservation confirm $RES_ID --payment pay_001

# Step 2: Check Redis — seats should be "sold"
docker exec redis-1 redis-cli -p 7001 -c HMGET "{event:$WT_EVENT}:seats" A1 A2
# Expected: "sold" "sold"

# Step 3: Check PostgreSQL — should also be "sold"
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status, sold_to FROM seats WHERE event_id = '$WT_EVENT' AND seat_id IN ('A1','A2');"
# Expected: Both sold

# Step 4: Verify reservation status in both stores
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, status, payment_id, confirmed_at FROM reservations WHERE id = '$RES_ID';"
# Expected: status=confirmed, payment_id=pay_001
```

### 6.4 Pros & Cons

| Pros | Cons |
|------|------|
| Strong consistency | Higher write latency (two writes) |
| Data always in both stores | Partial failure handling needed |
| Simple to understand | Unnecessary writes if data is rarely read |

---

## Section 7: Pattern 5 — Write-Behind / Write-Back (Async Writes)

### 7.1 Concept

Write to Redis **immediately**, then **asynchronously** flush to PostgreSQL in the background. This gives the fastest write performance but introduces an inconsistency window.

```
        ┌──────────┐
        │   App    │
        │  WRITE   │
        └────┬─────┘
             │
     1. Write to Redis (instant return)
             │
             ▼
        ┌─────────┐
        │  Redis  │
        │ (Cache) │
        └────┬────┘
             │
     2. Return success to app (fast!)
             │
             │  ···············
             │  · Background  ·
             │  · async flush ·
             │  ···············
             │
     3. Batch write to DB (later)
             │
             ▼
        ┌──────────┐
        │PostgreSQL│
        │  (DB)    │
        └──────────┘
```

### 7.2 Code Example — Write-Behind Buffer

```go
// WriteBehindBuffer collects writes and flushes them in batches
type WriteBehindBuffer struct {
    rdb      *redis.ClusterClient
    postgres *db.PostgresDB
    ctx      context.Context
    buffer   chan WriteOp
    wg       sync.WaitGroup
}

type WriteOp struct {
    Type      string // "seat_update", "reservation_insert", etc.
    EventID   string
    SeatID    string
    Status    string
    Timestamp time.Time
}

func NewWriteBehindBuffer(rdb *redis.ClusterClient, pg *db.PostgresDB, bufferSize int) *WriteBehindBuffer {
    wb := &WriteBehindBuffer{
        rdb:      rdb,
        postgres: pg,
        ctx:      context.Background(),
        buffer:   make(chan WriteOp, bufferSize),
    }
    // Start background flusher
    wb.wg.Add(1)
    go wb.flushLoop()
    return wb
}

// UpdateSeatStatus writes to Redis immediately, queues PG write
func (wb *WriteBehindBuffer) UpdateSeatStatus(eventID, seatID, status string) error {
    // Step 1: Write to Redis IMMEDIATELY (fast path)
    seatsKey := fmt.Sprintf("{event:%s}:seats", eventID)
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

// flushLoop processes buffered writes in batches
func (wb *WriteBehindBuffer) flushLoop() {
    defer wb.wg.Done()

    batch := make([]WriteOp, 0, 100)
    ticker := time.NewTicker(2 * time.Second) // Flush every 2 seconds
    defer ticker.Stop()

    for {
        select {
        case op := <-wb.buffer:
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

// flushBatch writes a batch of operations to PostgreSQL
func (wb *WriteBehindBuffer) flushBatch(batch []WriteOp) {
    tx, err := wb.postgres.DB.Begin()
    if err != nil {
        log.Printf("[Write-Behind] ERROR: Failed to begin transaction: %v", err)
        return
    }
    defer tx.Rollback()

    stmt, _ := tx.Prepare(`
        UPDATE seats SET status = $1, updated_at = $2
        WHERE event_id = $3 AND seat_id = $4`)
    defer stmt.Close()

    for _, op := range batch {
        switch op.Type {
        case "seat_update":
            _, err := stmt.Exec(op.Status, op.Timestamp, op.EventID, op.SeatID)
            if err != nil {
                log.Printf("[Write-Behind] ERROR: Failed to flush %s/%s: %v",
                    op.EventID, op.SeatID, err)
            }
        }
    }

    if err := tx.Commit(); err != nil {
        log.Printf("[Write-Behind] ERROR: Batch commit failed: %v", err)
        return
    }

    log.Printf("[Write-Behind] Flushed %d operations to PostgreSQL", len(batch))
}
```

### 7.3 Write-Behind with Redis Streams (Production-Grade)

```go
// Using Redis Streams as a durable write-behind queue
// This survives Redis restarts (Streams are persisted)

func (s *ReservationService) ReserveSeatWriteBehind(eventID, seatID, userID string) error {
    seatsKey := fmt.Sprintf("{event:%s}:seats", eventID)

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

    return nil // Return fast!
}

// Background consumer reads from Stream and writes to PostgreSQL
func (s *ReservationService) WriteBehindConsumer() {
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
                eventID := msg.Values["event_id"].(string)
                seatID := msg.Values["seat_id"].(string)
                status := msg.Values["status"].(string)

                // Write to PostgreSQL
                s.postgres.DB.Exec(`
                    UPDATE seats SET status = $1, updated_at = NOW()
                    WHERE event_id = $2 AND seat_id = $3`,
                    status, eventID, seatID,
                )

                lastID = msg.ID
            }
        }
    }
}
```

### 7.4 Hands-On Lab — Write-Behind

**Goal:** Simulate async writes using Redis Streams and observe the delay between Redis and PostgreSQL.

#### Exercise 1: Simulate Write-Behind with Redis Streams

```bash
# Step 1: Write to Redis immediately (the "fast" write)
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$EVENT_ID}:seats" "E1" "pending"
# Expected: (integer) 0 or 1

# Step 2: Publish to a Redis Stream (the "async queue")
docker exec redis-1 redis-cli -p 7001 -c XADD "write_behind:seat_updates" "*" \
  event_id "$EVENT_ID" seat_id "E1" status "pending" user_id "demo_user"
# Expected: a stream ID like "1710000000000-0"

# Step 3: Check Redis — data is already there (fast!)
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:$EVENT_ID}:seats" E1
# Expected: "pending"

# Step 4: Check PostgreSQL — data is NOT there yet (async!)
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status FROM seats WHERE event_id = '$EVENT_ID' AND seat_id = 'E1';"
# Expected: Still "available" (async flush hasn't happened)

# Step 5: Read the Stream (simulating the consumer)
docker exec redis-1 redis-cli -p 7001 -c XRANGE "write_behind:seat_updates" - + COUNT 5
# Expected: Stream entries with event_id, seat_id, status
```

#### Exercise 2: Observe the Inconsistency Window

```bash
# The key insight: between Step 1 (Redis write) and the async flush,
# Redis and PostgreSQL are INCONSISTENT.

# Redis says: E1 = "pending"
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:$EVENT_ID}:seats" E1

# PostgreSQL says: E1 = "available"
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT status FROM seats WHERE event_id = '$EVENT_ID' AND seat_id = 'E1';"

# This is the trade-off: faster writes, but temporary inconsistency.
# That's why we use Write-Through (not Write-Behind) for ticket reservations!

# Cleanup: restore the seat
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$EVENT_ID}:seats" "E1" "available"
docker exec redis-1 redis-cli -p 7001 -c DEL "write_behind:seat_updates"
```

### 7.5 Pros & Cons

| Pros | Cons |
|------|------|
| Fastest write performance | Data loss risk if Redis crashes before flush |
| Reduced DB load (batching) | Temporary inconsistency between Redis and PG |
| App doesn't wait for DB | Complex failure handling |
| Great for high-throughput writes | Need careful ordering guarantees |

### 7.6 When to Use

- Analytics events, view counters, click tracking
- IoT sensor data ingestion
- Log aggregation
- **NOT** for financial transactions (use Write-Through instead)

---

## Section 8: Pattern 6 — Write-Around

### 8.1 Concept

Write **only to the database**, skip the cache entirely. The cache is populated only on subsequent reads (via Cache-Aside or Read-Through).

```
        ┌──────────┐
        │   App    │
        │  WRITE   │
        └────┬─────┘
             │
             │ (skip Redis entirely)
             │
             ▼
        ┌──────────┐
        │PostgreSQL│
        │  (DB)    │
        └──────────┘

  Later, on READ:
        ┌──────────┐
        │   App    │
        │  READ    │
        └────┬─────┘
             │
     ┌───────┴───────┐
     │ Cache miss!   │
     ▼               │
  Load from PG ──► Cache in Redis
```

### 8.2 Code Example

```go
// Write-Around: Write only to PostgreSQL, invalidate Redis cache
func (s *ReservationService) CreateEventWriteAround(event *models.Event) error {
    // Write ONLY to PostgreSQL
    err := s.postgres.InsertEvent(event)
    if err != nil {
        return err
    }
    log.Printf("[Write-Around] Event %s written to PostgreSQL only", event.ID)

    // Optionally invalidate any stale cache
    eventKey := fmt.Sprintf("{event:%s}", event.ID)
    s.rdb.Del(s.ctx, eventKey)

    // Redis will be populated on the FIRST READ (via Cache-Aside)
    return nil
}

// Bulk import — perfect use case for Write-Around
func (s *ReservationService) BulkImportEvents(events []*models.Event) error {
    tx, _ := s.postgres.DB.Begin()
    defer tx.Rollback()

    for _, event := range events {
        tx.Exec(`INSERT INTO events (...) VALUES (...)`, event.ID, event.Name)
    }

    if err := tx.Commit(); err != nil {
        return err
    }

    log.Printf("[Write-Around] Bulk imported %d events to PostgreSQL", len(events))
    // Don't cache! Most of these may never be read
    return nil
}
```

### 8.3 Hands-On Lab — Write-Around

**Goal:** Simulate writing directly to PostgreSQL and observing that Redis has no data until a read occurs.

#### Exercise 1: Write Directly to PostgreSQL (Skip Redis)

```bash
# Step 1: Insert an event directly into PostgreSQL (Write-Around)
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "INSERT INTO events (id, name, venue, event_date, total_seats, rows, seats_per_row, price_per_seat, created_at)
   VALUES ('wa-test-01', 'Write-Around Event', 'Test Venue', NOW() + INTERVAL '30 days', 20, 4, 5, 25.00, NOW())
   ON CONFLICT DO NOTHING;"
# Expected: INSERT 0 1

# Step 2: Check Redis — nothing there!
docker exec redis-1 redis-cli -p 7001 -c GET "{event:wa-test-01}"
# Expected: (nil)

# Step 3: Verify in PostgreSQL — data is there
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, total_seats FROM events WHERE id = 'wa-test-01';"
# Expected: wa-test-01 | Write-Around Event | 20
```

#### Exercise 2: First Read Populates Cache (via Cache-Aside)

```bash
# Step 1: Read via the app — triggers Cache-Aside / Fallback
./ticket-reservation availability wa-test-01
# Expected: logs show [Fallback] — data loaded from PostgreSQL

# Step 2: Now check Redis
docker exec redis-1 redis-cli -p 7001 -c EXISTS "{event:wa-test-01}"
# The event key may now exist if the fallback populated it

# Cleanup
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "DELETE FROM events WHERE id = 'wa-test-01';"
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:wa-test-01}"
```

### 8.4 Pros & Cons

| Pros | Cons |
|------|------|
| Avoids polluting cache with unread data | First read always misses cache |
| Good for write-heavy, read-light data | Higher read latency on first access |
| Simpler write path | Not suitable for latency-sensitive reads |

---

## Section 9: All Patterns Comparison

### 9.1 Side-by-Side Overview

```
┌────────────────┬──────────────┬───────────────┬─────────────┬──────────────┐
│    Pattern     │  Write Path  │  Read Path    │ Consistency │  Complexity  │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Cache-Aside    │ App → DB     │ App → Redis   │ Eventual    │    Low       │
│                │ App → DEL    │ miss? → DB    │             │              │
│                │  Redis       │ → SET Redis   │             │              │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Read-Through   │ (same as     │ App → Cache   │ Eventual    │    Medium    │
│                │  Cache-Aside │ miss? Cache   │             │              │
│                │  for writes) │ auto-loads DB │             │              │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Refresh-Ahead  │ (same as     │ App → Cache   │ Near-real   │    High      │
│                │  above for   │ low TTL? BG   │ time        │              │
│                │  writes)     │ refresh       │             │              │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Write-Through  │ App → DB     │ App → Redis   │ Strong      │    Low       │
│                │ App → Redis  │ (always hit)  │             │              │
│                │ (sync)       │               │             │              │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Write-Behind   │ App → Redis  │ App → Redis   │ Eventual    │    High      │
│                │ BG → DB      │ (always hit)  │             │              │
│                │ (async)      │               │             │              │
├────────────────┼──────────────┼───────────────┼─────────────┼──────────────┤
│ Write-Around   │ App → DB     │ App → Redis   │ Eventual    │    Low       │
│                │ (skip Redis) │ miss? → DB    │             │              │
│                │              │ → SET Redis   │             │              │
└────────────────┴──────────────┴───────────────┴─────────────┴──────────────┘
```

### 9.2 Performance Characteristics

```
                 Write Latency          Read Latency (warm)    Read Latency (cold)
                 ────────────           ───────────────────    ───────────────────
Cache-Aside      N/A (writes go         ~1ms (Redis HIT)      ~5-50ms (PG + cache)
                  to DB only)

Read-Through     N/A (writes go         ~1ms (Redis HIT)      ~5-50ms (auto-load)
                  to DB only)

Refresh-Ahead    N/A                    ~1ms (always warm!)    ~5-50ms (first only)

Write-Through    ~5-50ms               ~1ms (always in cache)  N/A (always cached)
                 (DB + Redis)

Write-Behind     ~1ms                  ~1ms (always in cache)  N/A (always cached)
                 (Redis only!)

Write-Around     ~5-20ms              ~1ms (HIT)               ~5-50ms (PG + cache)
                 (DB only)
```

### 9.3 Decision Matrix

```
Need strong consistency?
├── YES → Write-Through (our CreateEvent, ConfirmReservation)
└── NO
    ├── Write-heavy?
    │   ├── YES → Write-Behind (analytics, counters, logs)
    │   └── NO → Write-Around (bulk imports, rarely-read data)
    │
    └── Read-heavy?
        ├── Hot keys, predictable access?
        │   └── YES → Refresh-Ahead (dashboard stats, popular events)
        └── Random access, long-tail?
            ├── Want app simplicity?
            │   └── YES → Read-Through (reservation history)
            └── Want full control?
                └── YES → Cache-Aside (event lookups)
```

---

## Section 10: Combining Patterns — Real-World Architecture

In production, you combine multiple patterns for different data types:

```
┌─────────────────────────────────────────────────────────────────────┐
│                   TICKET RESERVATION SYSTEM                         │
│                                                                     │
│  Event Creation     ──►  Write-Through  (strong consistency)        │
│  Seat Reservations  ──►  Write-Through  (financial accuracy)        │
│  Event Reads        ──►  Cache-Aside    (lazy load, TTL-based)      │
│  Dashboard Stats    ──►  Refresh-Ahead  (always-warm cache)         │
│  View Counters      ──►  Write-Behind   (high throughput, async)    │
│  Bulk Data Import   ──►  Write-Around   (skip cache, read later)    │
│  User History       ──►  Read-Through   (auto-load, transparent)    │
│  Consistency Check  ──►  Reconciliation (periodic sync)             │
└─────────────────────────────────────────────────────────────────────┘
```

### Which Patterns Our App Uses

| Operation | Code Function | Pattern Used | Why |
|-----------|--------------|-------------|-----|
| Create Event | `CreateEvent` | **Write-Through** | Need data in both stores immediately |
| Create Event | `CreateEventWriteAround` | **Write-Around** | Skip Redis, write PG only |
| Reserve Seats | `ReserveSeats` | **Write-Through** | Durable record of pending reservation |
| Reserve Seat | `ReserveSeatWriteBehind` | **Write-Behind** | Fast async via Redis Streams |
| Confirm | `ConfirmReservation` | **Write-Through** | Financial transaction — must be consistent |
| Cancel | `CancelReservation` | **Write-Through** | Must release seats in both stores |
| Get Event | `GetEventCacheAside` | **Cache-Aside** | App manages cache, loads from PG on miss |
| Get Event | `ReadThroughCache.GetEvent` | **Read-Through** | Cache auto-loads transparently |
| Get Event | `GetEventRefreshAhead` | **Refresh-Ahead** | Background refresh before TTL expires |
| Get Seats | `GetSeatsCacheAside` | **Cache-Aside** | Hash-based seat status cache |
| Update Event | `UpdateEventCacheAside` | **Cache Invalidation** | Delete cache on write |
| Bulk Import | `BulkImportEvents` | **Write-Around** | Skip cache for bulk data |
| Get Stats | `ReadThroughCache.GetEventStats` | **Read-Through** | Auto-load computed stats |
| Reconcile | `ReconcileReservations` | **Periodic Reconciliation** | Safety net for sync drift |

### Code Files

| File | Contains |
|------|---------|
| `app/service/reservation.go` | Core service + Write-Through + Fallback + Reconciliation |
| `app/service/caching_patterns.go` | All 6 caching pattern implementations |
| `app/api/server.go` | HTTP API with `?pattern=` query parameter support |
| `app/db/postgres.go` | PostgreSQL client wrapper |

---

## Section 11: Testing Patterns via HTTP API

### 11.1 Start the Server

```bash
# Start server with PostgreSQL integration
PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable" \
  ./ticket-reservation server --addr :8080
```

### 11.2 API Endpoints with Pattern Selection

Use the `?pattern=` query parameter to select which caching pattern to use:

#### Create Event

```bash
# Write-Through (default) — writes to both PG and Redis
curl -s -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{"name":"API Test","rows":3,"seats_per_row":5}' | jq .

# Write-Around — writes to PG only, skips Redis
curl -s -X POST "http://localhost:8080/events?pattern=write-around" \
  -H "Content-Type: application/json" \
  -d '{"name":"Write-Around Test","rows":2,"seats_per_row":4}' | jq .
```

#### Get Event

```bash
export EVENT_ID="<event-id>"

# Default (Fallback pattern)
curl -s http://localhost:8080/events/$EVENT_ID | jq .

# Cache-Aside — app checks Redis, loads from PG on miss
curl -s "http://localhost:8080/events/$EVENT_ID?pattern=cache-aside" | jq .

# Read-Through — cache auto-loads from PG
curl -s "http://localhost:8080/events/$EVENT_ID?pattern=read-through" | jq .

# Refresh-Ahead — background refresh when TTL is low
curl -s "http://localhost:8080/events/$EVENT_ID?pattern=refresh-ahead" | jq .
```

#### Get Availability

```bash
# Default (Fallback)
curl -s http://localhost:8080/events/$EVENT_ID/availability | jq .

# Read-Through — auto-load computed stats
curl -s "http://localhost:8080/events/$EVENT_ID/availability?pattern=read-through" | jq .
```

#### Get Seats

```bash
# Default — available seats only
curl -s http://localhost:8080/events/$EVENT_ID/seats | jq .

# Cache-Aside — all seats with full status map
curl -s "http://localhost:8080/events/$EVENT_ID/seats?pattern=cache-aside" | jq .
```

### 11.3 Supported Patterns per Endpoint

| Endpoint | `pattern=` | Function Called |
|----------|-----------|-----------------|
| `POST /events` | _(default)_ | `CreateEvent` (Write-Through) |
| `POST /events` | `write-around` | `CreateEventWriteAround` |
| `GET /events/:id` | _(default)_ | `GetEvent` (Fallback) |
| `GET /events/:id` | `cache-aside` | `GetEventCacheAside` |
| `GET /events/:id` | `read-through` | `ReadThroughCache.GetEvent` |
| `GET /events/:id` | `refresh-ahead` | `GetEventRefreshAhead` |
| `GET /events/:id/availability` | _(default)_ | `GetAvailability` (Fallback) |
| `GET /events/:id/availability` | `read-through` | `ReadThroughCache.GetEventStats` |
| `GET /events/:id/seats` | _(default)_ | `GetAvailableSeats` |
| `GET /events/:id/seats` | `cache-aside` | `GetSeatsCacheAside` |

---

## Section 12: Full Integration Lab — CLI Demo

### Exercise: End-to-End Workflow

Run the full `pg-demo` command to see Write-Through, Fallback, and Reconciliation in action:

```bash
# Step 1: Run the full PostgreSQL integration demo
PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable" \
  ./ticket-reservation pg-demo
```

Watch the log output — you'll see:
- `[Write-Through]` — event created in both stores
- `[Write-Through]` — reservation written to both stores
- `[Write-Through]` — confirmation written to both stores
- `[Reconciliation]` — checking Redis matches PostgreSQL

### Exercise: Reconciliation

```bash
# Step 1: Create an event and confirm a reservation
./ticket-reservation create-event --name "Reconcile Test" --rows 2 --seats 5 --price 50
export RECON_EVENT="<event-id>"

./ticket-reservation reserve --event $RECON_EVENT --user user1 --seats A1,A2
export RECON_RES="<reservation-id>"

./ticket-reservation confirm $RECON_RES --payment pay_recon

# Step 2: Manually break Redis to simulate drift
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$RECON_EVENT}:seats" A1 "available"
# Now Redis says A1 is "available" but PostgreSQL says "sold" — MISMATCH!

# Step 3: Run reconciliation to fix it
./ticket-reservation reconcile $RECON_EVENT

# Expected output:
#   [Reconciliation] MISMATCH: Seat A1 is 'available' in Redis but confirmed in PG
#   [Reconciliation] Complete: checked N seats, fixed 1 mismatches

# Step 4: Verify Redis is fixed
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:$RECON_EVENT}:seats" A1
# Expected: "sold" (fixed by reconciliation!)
```

---

## Section 13: Cleanup

```bash
# Stop all services
cd /path/to/redis-cluster-lab
docker-compose down

# To also remove volumes (PostgreSQL data):
docker-compose down -v
```

---

## Appendix A: Quick Reference

| Pattern | Read | Write | Best For |
|---------|------|-------|----------|
| **Cache-Aside** | App checks cache, loads on miss | App writes DB, deletes cache | General purpose |
| **Read-Through** | Cache auto-loads from DB | Same as Cache-Aside | Clean APIs |
| **Refresh-Ahead** | BG refresh before TTL expires | Same as above | Hot data |
| **Write-Through** | Always from cache (warm) | Sync write to both | Critical data |
| **Write-Behind** | Always from cache (warm) | Async batch to DB | High throughput |
| **Write-Around** | Cache-Aside on first read | Write only to DB | Bulk imports |

## Appendix B: Key Redis Commands Used

```bash
# STRING (event data, reservation data)
SET key value EX ttl        # Set with expiry
GET key                     # Get value
DEL key                     # Delete (invalidate)
TTL key                     # Check remaining TTL

# HASH (seat statuses, stats)
HSET key field value        # Set hash field
HGET key field              # Get hash field
HGETALL key                 # Get all fields
HMGET key f1 f2 f3          # Get multiple fields
HINCRBY key field amount    # Atomic increment

# SORTED SET (waitlist)
ZADD key score member       # Add to sorted set
ZRANGE key start stop       # Get range

# STREAM (write-behind queue)
XADD stream * field value   # Add to stream
XREAD COUNT n STREAMS s id  # Read from stream
XRANGE stream - + COUNT n   # Read range

# PIPELINE (batch commands)
MULTI / EXEC                # Transaction
Pipeline                    # Batch without transaction
```

## Appendix C: Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PG_DSN` | (none) | PostgreSQL connection string |
| `REDIS_NODES` | `localhost:7001,...` | Redis cluster node addresses |

Default PG_DSN for this lab:
```bash
export PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable"
```

## Appendix D: Data Structures — Redis vs PostgreSQL

This appendix shows the exact data structure for every entity in **both** stores, so you can see how the same data is represented differently.

---

### D.1 Event

#### Go Struct (`app/models/models.go`)

```go
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
```

#### Redis — STRING (JSON)

```
Key:    {event:<event-id>}
Type:   STRING
TTL:    none (persistent) or 1 hour (Cache-Aside)
```

```json
{
  "id": "a1b2c3d4",
  "name": "Rock Concert",
  "venue": "Main Hall",
  "date": "2026-04-15T20:00:00Z",
  "total_seats": 50,
  "rows": 5,
  "seats_per_row": 10,
  "price_per_seat": 100.00,
  "created_at": "2026-03-15T10:30:00Z"
}
```

```bash
# Read event from Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:a1b2c3d4}"

# Check which slot/node stores this key
docker exec redis-1 redis-cli -p 7001 -c CLUSTER KEYSLOT "{event:a1b2c3d4}"
```

#### PostgreSQL — `events` Table

```sql
CREATE TABLE events (
    id             VARCHAR(36) PRIMARY KEY,
    name           VARCHAR(255) NOT NULL,
    venue          VARCHAR(255) NOT NULL,
    event_date     TIMESTAMP NOT NULL,
    total_seats    INTEGER NOT NULL,
    rows           INTEGER NOT NULL,
    seats_per_row  INTEGER NOT NULL,
    price_per_seat NUMERIC(10,2) NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT NOW()
);
```

```
 id       | name         | venue     | event_date          | total_seats | rows | seats_per_row | price_per_seat | created_at
----------+--------------+-----------+---------------------+-------------+------+---------------+----------------+---------------------
 a1b2c3d4 | Rock Concert | Main Hall | 2026-04-15 20:00:00 |          50 |    5 |            10 |         100.00 | 2026-03-15 10:30:00
```

```bash
# Read event from PostgreSQL
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT * FROM events WHERE id = 'a1b2c3d4';"
```

---

### D.2 Seats

#### Go Struct (`app/models/models.go`)

```go
type Seat struct {
    ID       string     `json:"id"`        // e.g., "A1", "B15"
    EventID  string     `json:"event_id"`
    Row      string     `json:"row"`
    Number   int        `json:"number"`
    Status   SeatStatus `json:"status"`    // available | pending | reserved | sold
    Price    float64    `json:"price"`
    HeldBy   string     `json:"held_by,omitempty"`
    HeldAt   *time.Time `json:"held_at,omitempty"`
    SoldTo   string     `json:"sold_to,omitempty"`
    SoldAt   *time.Time `json:"sold_at,omitempty"`
}

type SeatStatus string
const (
    SeatAvailable SeatStatus = "available"
    SeatPending   SeatStatus = "pending"
    SeatReserved  SeatStatus = "reserved"
    SeatSold      SeatStatus = "sold"
)
```

#### Redis — HASH (field = seat ID, value = status)

```
Key:    {event:<event-id>}:seats
Type:   HASH
TTL:    none (persistent) or 30 min (Cache-Aside)
```

```
Field  │ Value
───────┼───────────
A1     │ sold
A2     │ sold
A3     │ available
A4     │ available
A5     │ pending
B1     │ available
B2     │ available
...    │ ...
```

> **Note:** Redis stores only `seat_id → status`. The full seat details (row, number, price, held_by, sold_to) are stored in PostgreSQL.

```bash
# Read all seats for an event
docker exec redis-1 redis-cli -p 7001 -c HGETALL "{event:a1b2c3d4}:seats"

# Read specific seat status
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:a1b2c3d4}:seats" A1

# Count seats by status (pipeline)
docker exec redis-1 redis-cli -p 7001 -c HVALS "{event:a1b2c3d4}:seats"
```

#### PostgreSQL — `seats` Table

```sql
CREATE TABLE seats (
    event_id    VARCHAR(36) NOT NULL REFERENCES events(id),
    seat_id     VARCHAR(10) NOT NULL,
    row_letter  VARCHAR(1) NOT NULL,
    seat_number INTEGER NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'available',
    price       NUMERIC(10,2) NOT NULL,
    held_by     VARCHAR(36),           -- user_id who holds the seat
    sold_to     VARCHAR(36),           -- user_id who bought the seat
    updated_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, seat_id)
);

CREATE INDEX idx_seats_event_status ON seats(event_id, status);
```

```
 event_id | seat_id | row_letter | seat_number | status    | price  | held_by | sold_to | updated_at
----------+---------+------------+-------------+-----------+--------+---------+---------+---------------------
 a1b2c3d4 | A1      | A          |           1 | sold      | 100.00 |         | user1   | 2026-03-15 10:35:00
 a1b2c3d4 | A2      | A          |           2 | sold      | 100.00 |         | user1   | 2026-03-15 10:35:00
 a1b2c3d4 | A3      | A          |           3 | available | 100.00 |         |         | 2026-03-15 10:30:00
 a1b2c3d4 | A4      | A          |           4 | available | 100.00 |         |         | 2026-03-15 10:30:00
 a1b2c3d4 | A5      | A          |           5 | pending   | 100.00 | user2   |         | 2026-03-15 10:36:00
```

```bash
# Read all seats for an event
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status, held_by, sold_to FROM seats WHERE event_id = 'a1b2c3d4' ORDER BY seat_id;"

# Count by status
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT status, COUNT(*) FROM seats WHERE event_id = 'a1b2c3d4' GROUP BY status;"
```

#### Redis vs PostgreSQL — Seats Comparison

```
┌────────────────────────────────────────────────────────────────────┐
│                    SEATS DATA COMPARISON                           │
├──────────────────────────┬─────────────────────────────────────────┤
│       Redis (HASH)       │          PostgreSQL (TABLE)             │
├──────────────────────────┼─────────────────────────────────────────┤
│ Key: {event:ID}:seats    │ Table: seats                           │
│ Field: seat_id           │ Columns: seat_id, row_letter,          │
│ Value: status only       │   seat_number, status, price,          │
│                          │   held_by, sold_to, updated_at         │
├──────────────────────────┼─────────────────────────────────────────┤
│ Fast O(1) per seat       │ Rich queries (GROUP BY, JOIN)          │
│ Atomic via Lua scripts   │ ACID transactions                      │
│ No history               │ Full audit trail (updated_at)          │
│ ~0.5ms per HGET          │ ~5ms per SELECT                        │
└──────────────────────────┴─────────────────────────────────────────┘
```

---

### D.3 Reservation

#### Go Struct (`app/models/models.go`)

```go
type Reservation struct {
    ID            string            `json:"id"`
    EventID       string            `json:"event_id"`
    UserID        string            `json:"user_id"`
    Seats         []string          `json:"seats"`          // ["A1","A2","A3"]
    Status        ReservationStatus `json:"status"`         // pending | confirmed | cancelled | expired
    TotalAmount   float64           `json:"total_amount"`
    CreatedAt     time.Time         `json:"created_at"`
    ExpiresAt     time.Time         `json:"expires_at"`
    ConfirmedAt   *time.Time        `json:"confirmed_at,omitempty"`
    CancelledAt   *time.Time        `json:"cancelled_at,omitempty"`
    PaymentID     string            `json:"payment_id,omitempty"`
    CustomerEmail string            `json:"customer_email,omitempty"`
    CustomerName  string            `json:"customer_name,omitempty"`
}

type ReservationStatus string
const (
    ReservationPending   ReservationStatus = "pending"
    ReservationConfirmed ReservationStatus = "confirmed"
    ReservationCancelled ReservationStatus = "cancelled"
    ReservationExpired   ReservationStatus = "expired"
)
```

#### Redis — STRING (JSON) with TTL

```
Key:    reservation:<reservation-id>
Type:   STRING
TTL:    15 minutes (pending) or none (confirmed) or 24 hours (cancelled)
```

```json
{
  "id": "abc123def456",
  "event_id": "a1b2c3d4",
  "user_id": "user1",
  "seats": ["A1", "A2", "A3"],
  "status": "confirmed",
  "total_amount": 300.00,
  "created_at": "2026-03-15T10:32:00Z",
  "expires_at": "2026-03-15T10:47:00Z",
  "confirmed_at": "2026-03-15T10:35:00Z",
  "payment_id": "pay_demo_001",
  "customer_email": "alice@example.com",
  "customer_name": "Alice"
}
```

> **Note:** The `seats` array is embedded in the JSON. Redis stores the complete reservation as a single JSON string.

```bash
# Read reservation from Redis
docker exec redis-1 redis-cli -p 7001 -c GET "reservation:abc123def456"

# Check TTL (pending reservations expire after 15 minutes)
docker exec redis-1 redis-cli -p 7001 -c TTL "reservation:abc123def456"
```

#### Redis — Supporting Sets

```
Key:    {event:<event-id>}:reservations
Type:   SET
Value:  reservation IDs belonging to this event
```

```bash
# List all reservation IDs for an event
docker exec redis-1 redis-cli -p 7001 -c SMEMBERS "{event:a1b2c3d4}:reservations"
# Output: "abc123def456", "xyz789ghi012"
```

```
Key:    user:<user-id>:reservations
Type:   SET
Value:  reservation IDs belonging to this user
```

```bash
# List all reservation IDs for a user
docker exec redis-1 redis-cli -p 7001 -c SMEMBERS "user:user1:reservations"
```

#### PostgreSQL — `reservations` + `reservation_seats` Tables

```sql
CREATE TABLE reservations (
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

CREATE TABLE reservation_seats (
    reservation_id VARCHAR(36) NOT NULL REFERENCES reservations(id),
    event_id       VARCHAR(36) NOT NULL,
    seat_id        VARCHAR(10) NOT NULL,
    PRIMARY KEY (reservation_id, seat_id),
    FOREIGN KEY (event_id, seat_id) REFERENCES seats(event_id, seat_id)
);

CREATE INDEX idx_reservations_event ON reservations(event_id);
CREATE INDEX idx_reservations_user ON reservations(user_id);
CREATE INDEX idx_reservations_status ON reservations(status);
CREATE INDEX idx_reservation_seats_event ON reservation_seats(event_id, seat_id);
```

**`reservations` table:**
```
 id           | event_id | user_id | status    | total_amount | customer_name | payment_id    | created_at          | expires_at          | confirmed_at
--------------+----------+---------+-----------+--------------+---------------+---------------+---------------------+---------------------+---------------------
 abc123def456 | a1b2c3d4 | user1   | confirmed |       300.00 | Alice         | pay_demo_001  | 2026-03-15 10:32:00 | 2026-03-15 10:47:00 | 2026-03-15 10:35:00
 xyz789ghi012 | a1b2c3d4 | user2   | cancelled |       200.00 | Bob           |               | 2026-03-15 10:33:00 | 2026-03-15 10:48:00 |
```

**`reservation_seats` join table:**
```
 reservation_id | event_id | seat_id
----------------+----------+---------
 abc123def456   | a1b2c3d4 | A1
 abc123def456   | a1b2c3d4 | A2
 abc123def456   | a1b2c3d4 | A3
 xyz789ghi012   | a1b2c3d4 | B5
 xyz789ghi012   | a1b2c3d4 | B6
```

> **Key difference:** Redis embeds seats as a JSON array inside the reservation. PostgreSQL normalizes them into a separate `reservation_seats` join table with foreign keys.

```bash
# Read reservation with seats from PostgreSQL
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT r.id, r.status, r.total_amount, r.customer_name, r.payment_id,
          array_agg(rs.seat_id ORDER BY rs.seat_id) AS seats
   FROM reservations r
   JOIN reservation_seats rs ON rs.reservation_id = r.id
   WHERE r.id = 'abc123def456'
   GROUP BY r.id;"
```

#### Redis vs PostgreSQL — Reservation Comparison

```
┌────────────────────────────────────────────────────────────────────┐
│                 RESERVATION DATA COMPARISON                        │
├──────────────────────────┬─────────────────────────────────────────┤
│     Redis (STRING)       │       PostgreSQL (TABLE + JOIN)         │
├──────────────────────────┼─────────────────────────────────────────┤
│ Key: reservation:<id>    │ Tables: reservations +                  │
│ Value: full JSON         │   reservation_seats                     │
│ Seats: embedded array    │ Seats: normalized join table            │
│ TTL: 15min/none/24hr     │ No TTL (permanent)                     │
├──────────────────────────┼─────────────────────────────────────────┤
│ Fast single-key read     │ Rich queries (by user, event, status)  │
│ Auto-expire pending      │ Never auto-deletes                     │
│ No JOIN support          │ JOIN with events, seats tables          │
│ ~0.5ms per GET           │ ~5ms per SELECT with JOIN              │
└──────────────────────────┴─────────────────────────────────────────┘
```

---

### D.4 Event Stats

#### Go Struct (`app/models/models.go`)

```go
type EventStats struct {
    EventID        string  `json:"event_id"`
    TotalSeats     int     `json:"total_seats"`
    AvailableSeats int     `json:"available_seats"`
    PendingSeats   int     `json:"pending_seats"`
    SoldSeats      int     `json:"sold_seats"`
    WaitlistCount  int     `json:"waitlist_count"`
    Revenue        float64 `json:"revenue"`
}
```

#### Redis — HASH (pre-computed counters)

```
Key:    {event:<event-id>}:stats
Type:   HASH
TTL:    none (updated atomically by Lua scripts)
```

```
Field           │ Value
────────────────┼──────
total_seats     │ 50
available_seats │ 45
pending_seats   │ 2
sold_seats      │ 3
revenue         │ 300.00
```

```bash
# Read stats
docker exec redis-1 redis-cli -p 7001 -c HGETALL "{event:a1b2c3d4}:stats"

# Read single field
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:a1b2c3d4}:stats" sold_seats
```

#### PostgreSQL — Computed from `seats` Table (no dedicated table)

```sql
-- Stats are computed on-the-fly from the seats table
SELECT
    COUNT(*) AS total_seats,
    COUNT(*) FILTER (WHERE status = 'available') AS available_seats,
    COUNT(*) FILTER (WHERE status = 'pending') AS pending_seats,
    COUNT(*) FILTER (WHERE status = 'sold') AS sold_seats,
    COALESCE(SUM(price) FILTER (WHERE status = 'sold'), 0) AS revenue
FROM seats
WHERE event_id = 'a1b2c3d4';
```

```
 total_seats | available_seats | pending_seats | sold_seats | revenue
-------------+-----------------+---------------+------------+---------
          50 |              45 |             2 |          3 |  300.00
```

> **Key difference:** Redis stores stats as pre-computed counters (updated atomically by Lua scripts on each reservation/confirmation). PostgreSQL computes stats on-the-fly from the `seats` table using aggregate queries.

---

### D.5 Waitlist

#### Go Struct (`app/models/models.go`)

```go
type WaitlistEntry struct {
    ID             string     `json:"id"`
    EventID        string     `json:"event_id"`
    UserID         string     `json:"user_id"`
    RequestedSeats int        `json:"requested_seats"`
    Email          string     `json:"email"`
    JoinedAt       time.Time  `json:"joined_at"`
    NotifiedAt     *time.Time `json:"notified_at,omitempty"`
    Priority       int64      `json:"priority"`   // Unix timestamp for FIFO
}
```

#### Redis — SORTED SET (score = priority timestamp)

```
Key:    {event:<event-id>}:waitlist
Type:   SORTED SET (ZSET)
TTL:    none
Score:  Unix nanosecond timestamp (FIFO ordering)
Member: JSON-encoded WaitlistEntry
```

```bash
# View waitlist entries (FIFO order)
docker exec redis-1 redis-cli -p 7001 -c ZRANGE "{event:a1b2c3d4}:waitlist" 0 -1

# Count waitlist size
docker exec redis-1 redis-cli -p 7001 -c ZCARD "{event:a1b2c3d4}:waitlist"
```

> **Note:** The waitlist is currently Redis-only. It is not stored in PostgreSQL.

---

### D.6 All Redis Keys Summary

```
┌──────────────────────────────────────┬──────────┬─────────────────────────────┐
│ Key Pattern                          │ Type     │ Description                 │
├──────────────────────────────────────┼──────────┼─────────────────────────────┤
│ {event:<id>}                         │ STRING   │ Event JSON                  │
│ {event:<id>}:seats                   │ HASH     │ seat_id → status            │
│ {event:<id>}:stats                   │ HASH     │ Precomputed counters        │
│ {event:<id>}:reservations            │ SET      │ Reservation IDs for event   │
│ {event:<id>}:waitlist                │ ZSET     │ Waitlist entries (FIFO)     │
│ reservation:<id>                     │ STRING   │ Reservation JSON            │
│ user:<id>:reservations               │ SET      │ Reservation IDs for user    │
│ write_behind:seat_updates            │ STREAM   │ Write-Behind queue          │
│ {event:<id>}:computed_stats          │ STRING   │ Read-Through cached stats   │
├──────────────────────────────────────┼──────────┼─────────────────────────────┤
│ Keys with {event:<id>} hash tag      │          │ Co-located on same node     │
│ Keys without hash tag                │          │ Distributed across nodes    │
└──────────────────────────────────────┴──────────┴─────────────────────────────┘
```

### D.7 PostgreSQL ER Diagram

```
┌──────────────┐       ┌──────────────────┐       ┌──────────────────┐
│   events     │       │     seats        │       │  reservations    │
├──────────────┤       ├──────────────────┤       ├──────────────────┤
│ id       PK  │◄──┐   │ event_id  PK,FK │───┐   │ id          PK   │
│ name         │   │   │ seat_id   PK    │   │   │ event_id    FK   │──►events
│ venue        │   │   │ row_letter      │   │   │ user_id          │
│ event_date   │   │   │ seat_number     │   │   │ status           │
│ total_seats  │   │   │ status          │   │   │ total_amount     │
│ rows         │   │   │ price           │   │   │ customer_name    │
│ seats_per_row│   │   │ held_by         │   │   │ customer_email   │
│ price_per_seat│  │   │ sold_to         │   │   │ payment_id       │
│ created_at   │   │   │ updated_at      │   │   │ created_at       │
└──────────────┘   │   └──────────────────┘   │   │ expires_at       │
                   │                          │   │ confirmed_at     │
                   │                          │   │ cancelled_at     │
                   │                          │   └──────────────────┘
                   │                          │           │
                   │   ┌──────────────────┐   │           │
                   │   │reservation_seats │   │           │
                   │   ├──────────────────┤   │           │
                   │   │ reservation_id PK,FK │───────────┘
                   └───│ event_id       FK │───┘
                       │ seat_id     PK,FK │
                       └──────────────────┘
```
