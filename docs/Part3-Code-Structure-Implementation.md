# Part 3: Code Structure & Implementation

## Overview

This part explains the project structure, key code components, and how data flows through the application when interacting with Redis Cluster.

---

## 1. Project Structure

```
redis-cluster-lab/
├── docker-compose.yml      # Redis cluster infrastructure
├── redis.conf              # Redis configuration
├── Makefile                # Lab commands
│
├── app/
│   ├── main.go             # CLI entry point & command routing
│   ├── go.mod              # Go module definition
│   ├── go.sum              # Go module checksums
│   │
│   ├── cluster/
│   │   └── client.go       # Redis cluster client wrapper
│   │
│   ├── models/
│   │   └── models.go       # Data structures
│   │
│   ├── service/
│   │   └── reservation.go  # Business logic
│   │
│   └── cmd/
│       ├── commands.go     # Reservation commands (create, reserve, confirm, cancel, etc.)
│       └── sharding.go     # Sharding demos (slot-info, hash-tag, cross-slot, etc.)
│
├── scripts/
│   ├── init-cluster.sh        # Initialize cluster
│   ├── scale-add-master.sh    # Add master node
│   └── failover-test.sh       # Test failover
│
└── docs/
    └── ...                    # Documentation
```

---

## 2. Component Responsibilities

### cluster/client.go
**Purpose**: Handle connection to Redis Cluster with Docker IP mapping

Key responsibilities:
- Address mapping for Docker environments (remapAddress)
- Connection pool management with custom dialer
- Cluster information retrieval (GetClusterInfo, GetClusterNodes)
- Slot and node operations (GetSlotForKey, GetNodeForSlot)
- Health checking (Ping)
- Iteration helpers (ForEachMaster, ForEachShard)

### models/models.go
**Purpose**: Define data structures used throughout the application

Key structures:
- Event: Event metadata (ID, name, venue, date, seats config, pricing)
- Seat: Seat status information (ID, status, price, held/sold info)
- Reservation: Booking details (ID, event, user, seats, status, amounts, timestamps)
- WaitlistEntry: Waitlist queue item (ID, event, user, requested seats, priority)
- EventStats: Availability statistics (total, available, pending, sold, revenue)
- ClusterNode: Redis cluster node info (ID, address, role, slots)
- ClusterInfo: Cluster state information (state, slots assigned, nodes)

### service/reservation.go
**Purpose**: Core business logic for reservations

Key features:
- Lua scripts for atomic operations (reserve, confirm, release)
- Reservation workflow: CreateEvent → ReserveSeats → ConfirmReservation
- Cancellation with seat release (CancelReservation)
- Waitlist management (JoinWaitlist, ProcessWaitlist)
- TTL handling for pending reservations (auto-expiry)
- Statistics and availability (GetAvailability, GetAvailableSeats)
- Visual seat map display (PrintSeatMap)

### cmd/commands.go
**Purpose**: Core reservation CLI commands

Key functions:
- ClusterInfo: Display Redis cluster status
- CreateEvent: Initialize new events with seat grid
- ListEvents: Scan cluster for all events
- ReserveSeats: Book seats for a user
- ConfirmReservation: Complete pending booking
- CancelReservation: Cancel and release seats
- GetAvailability: Check seat statistics
- ShowSeatMap: Display visual seat grid
- JoinWaitlist: Add user to event waitlist
- RunDemo: Full demonstration scenario
- LoadTest: Concurrent reservation load test

### cmd/sharding.go
**Purpose**: Sharding demonstration and analysis commands

Key functions:
- SlotInfo: Display slot distribution across nodes
- KeySlot: Show which slot/node keys map to
- HashTagDemo: Demonstrate hash tags for co-location
- CrossSlotDemo: Demonstrate cross-slot limitations
- AnalyzeDistribution: Analyze key distribution in cluster
- ShardingDemo: Comprehensive sharding demonstration
- ReshardDemo: Explain resharding process
- SimulateHotKey: Hot key detection simulation
- MigrationDemo: Key migration explanation

---

## 3. Cluster Client Implementation

### Address Mapping for Docker

The core challenge: Redis nodes announce Docker internal IPs.

```go
// cluster/client.go

// addressMapper maps internal Docker IPs to localhost for host access
var addressMapper = map[string]string{
    "172.30.0.11": "127.0.0.1",
    "172.30.0.12": "127.0.0.1",
    "172.30.0.13": "127.0.0.1",
    "172.30.0.14": "127.0.0.1",
    "172.30.0.15": "127.0.0.1",
    "172.30.0.16": "127.0.0.1",
    "172.30.0.17": "127.0.0.1",
    "172.30.0.18": "127.0.0.1",
}

// remapAddress converts internal Docker IP:port to localhost:port
func remapAddress(addr string) string {
    host, port, err := net.SplitHostPort(addr)
    if err != nil {
        return addr
    }
    if mapped, ok := addressMapper[host]; ok {
        return net.JoinHostPort(mapped, port)
    }
    return addr
}
```

### Custom Dialer Configuration

```go
// cluster/client.go

// DefaultClusterAddrs returns the default cluster node addresses
func DefaultClusterAddrs() []string {
    return []string{
        "127.0.0.1:7001",
        "127.0.0.1:7002",
        "127.0.0.1:7003",
        "127.0.0.1:7004",
        "127.0.0.1:7005",
        "127.0.0.1:7006",
    }
}

// NewClient creates a new Redis cluster client
func NewClient(addrs []string) (*Client, error) {
    if len(addrs) == 0 {
        addrs = DefaultClusterAddrs()
    }

    rdb := redis.NewClusterClient(&redis.ClusterOptions{
        Addrs:           addrs,
        MaxRetries:      5,
        MinRetryBackoff: 100 * time.Millisecond,
        MaxRetryBackoff: 500 * time.Millisecond,
        DialTimeout:     5 * time.Second,
        ReadTimeout:     3 * time.Second,
        WriteTimeout:    3 * time.Second,
        PoolSize:        10,
        MinIdleConns:    5,
        // Route read commands to replicas for better distribution
        RouteRandomly: true,
        // Custom dialer to remap Docker internal IPs to localhost
        Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
            mappedAddr := remapAddress(addr)
            netDialer := &net.Dialer{
                Timeout:   5 * time.Second,
                KeepAlive: 5 * time.Minute,
            }
            return netDialer.DialContext(ctx, network, mappedAddr)
        },
    })
    // ... connection test with retries
}
```

---

## 4. Atomic Operations with Lua Scripts

### Why Lua Scripts?

In Redis Cluster, you cannot use standard transactions across multiple keys unless they're on the same node. Lua scripts solve this by:

1. Executing atomically on the server
2. No race conditions between check and update
3. All operations complete or none do

### Reserve Seats Script

```go
// service/reservation.go

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
```

### Executing the Script

```go
// service/reservation.go

func (s *ReservationService) ReserveSeats(eventID, userID string, seatIDs []string,
    customerName, customerEmail string) (*models.Reservation, error) {

    reservationID := uuid.New().String()[:12]
    now := time.Now()
    expiresAt := now.Add(s.reservationTTL)

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

    // All keys use same hash tag for co-location
    seatsKey := fmt.Sprintf("{event:%s}:seats", eventID)
    statsKey := fmt.Sprintf("{event:%s}:stats", eventID)

    // Execute Lua script
    result, err := reserveScript.Run(s.ctx, s.rdb,
        []string{seatsKey, statsKey}, args...).Slice()
    if err != nil {
        return nil, fmt.Errorf("failed to reserve seats: %w", err)
    }

    // Check result: {1, reservation_id} for success, {0, 'seat_unavailable', seat_id} for failure
    if result[0].(int64) == 0 {
        return nil, fmt.Errorf("seat %s is not available", result[2].(string))
    }

    // Create and store reservation record...
}
```

---

## 5. Data Flow Diagram

### Complete Reservation Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                    RESERVATION FLOW                              │
└──────────────────────────────────────────────────────────────────┘

User Request                     Application                    Redis Cluster
     │                               │                               │
     │  reserve A1,A2,A3             │                               │
     │  for event:abc123             │                               │
     │ ─────────────────────────────►                                │
     │                               │                               │
     │                               │  1. Calculate slot            │
     │                               │     {event:abc123} → 7186     │
     │                               │                               │
     │                               │  2. Find node for slot 7186   │
     │                               │     → 172.30.0.12:7002        │
     │                               │                               │
     │                               │  3. Execute Lua script        │
     │                               │ ─────────────────────────────►│
     │                               │                               │
     │                               │     EVALSHA <script>          │
     │                               │       KEYS[1]: {event:abc123}:seats
     │                               │       KEYS[2]: {event:abc123}:stats
     │                               │       ARGV: [res_id, user, ts, 3, A1, A2, A3]
     │                               │                               │
     │                               │                    ┌──────────┤
     │                               │                    │ Lua runs │
     │                               │                    │atomically│
     │                               │                    │          │
     │                               │                    │ 1. Check │
     │                               │                    │    seats │
     │                               │                    │          │
     │                               │                    │ 2. Mark  │
     │                               │                    │  pending │
     │                               │                    └──────────┤
     │                               │                               │
     │                               │  4. Response: OK              │
     │                               │ ◄─────────────────────────────│
     │                               │                               │
     │  Reservation confirmed        │                               │
     │ ◄─────────────────────────────                                │
     │                               │                               │
```

### Step-by-Step Breakdown

| Step | Component | Action |
|------|-----------|--------|
| 1 | User | Sends reserve request for seats A1, A2, A3 |
| 2 | Application | Calculates hash slot from `{event:abc123}` |
| 3 | Cluster Client | Routes request to correct node (Node 2) |
| 4 | Redis Node | Executes Lua script atomically |
| 5 | Lua Script | Checks all seats are "available" |
| 6 | Lua Script | Marks seats as "pending" |
| 7 | Lua Script | Updates availability statistics |
| 8 | Redis Node | Returns success response |
| 9 | Application | Creates reservation record |
| 10 | User | Receives confirmation |

---

## 6. Key Code Patterns

### Pattern 1: Hash Tag for Key Generation

```go
// service/reservation.go

const (
    // Key patterns - using hash tags {event:ID} to ensure related keys are in the same slot
    eventKeyPattern        = "{event:%s}"              // Event metadata
    seatsKeyPattern        = "{event:%s}:seats"        // Hash of seat statuses
    reservationsKeyPattern = "{event:%s}:reservations" // Set of reservation IDs
    waitlistKeyPattern     = "{event:%s}:waitlist"     // Sorted set for waitlist
    reservationKeyPattern  = "reservation:%s"          // Individual reservation data
    userReservationsKey    = "user:%s:reservations"    // User's reservations
    statsKeyPattern        = "{event:%s}:stats"        // Event statistics
)

// Usage example:
eventKey := fmt.Sprintf(eventKeyPattern, eventID)       // {event:abc123}
seatsKey := fmt.Sprintf(seatsKeyPattern, eventID)       // {event:abc123}:seats
statsKey := fmt.Sprintf(statsKeyPattern, eventID)       // {event:abc123}:stats
```

### Pattern 2: TTL for Pending Reservations

```go
// service/reservation.go

const (
    // Default reservation hold time (15 minutes)
    DefaultReservationTTL = 15 * time.Minute
)

// ReservationService handles ticket reservation operations
type ReservationService struct {
    rdb            *redis.ClusterClient
    ctx            context.Context
    reservationTTL time.Duration
}

// Store reservation with TTL
resKey := fmt.Sprintf(reservationKeyPattern, reservationID)
pipe.Set(s.ctx, resKey, resJSON, s.reservationTTL)  // Expires automatically

// For confirmed reservations, remove TTL
s.rdb.Set(s.ctx, resKey, resJSON, 0)  // No expiry for confirmed
```

### Pattern 3: Pipeline for Multiple Independent Operations

```go
// service/reservation.go

// GetAvailability returns event availability statistics
func (s *ReservationService) GetAvailability(eventID string) (*models.EventStats, error) {
    statsKey := fmt.Sprintf(statsKeyPattern, eventID)
    waitlistKey := fmt.Sprintf(waitlistKeyPattern, eventID)

    pipe := s.rdb.Pipeline()
    statsCmd := pipe.HGetAll(s.ctx, statsKey)
    waitlistCmd := pipe.ZCard(s.ctx, waitlistKey)

    _, err := pipe.Exec(s.ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to get availability: %w", err)
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
```

### Pattern 4: Error Handling from Lua Scripts

```go
// service/reservation.go

// Lua scripts return array results for structured responses
// Reserve script returns: {1, reservation_id} for success
//                        {0, 'seat_unavailable', seat_id} for failure

result, err := reserveScript.Run(s.ctx, s.rdb,
    []string{seatsKey, statsKey}, args...).Slice()
if err != nil {
    return nil, fmt.Errorf("failed to reserve seats: %w", err)
}

// Check first element for success/failure
if result[0].(int64) == 0 {
    return nil, fmt.Errorf("seat %s is not available", result[2].(string))
}

// Success - result[1] contains reservation_id
```

---

## 7. Summary

| Component | Purpose | Key Implementation |
|-----------|---------|-------------------|
| **Client** | Connect to cluster | Custom dialer with IP mapping |
| **Models** | Data structures | Go structs matching Redis data |
| **Service** | Business logic | Lua scripts for atomicity |
| **Commands** | CLI interface | Thin wrapper around service |

### Critical Implementation Points

1. **Always use hash tags** for related keys
2. **Use Lua scripts** for multi-key atomic operations
3. **Handle address mapping** in Docker environments
4. **Set TTLs** on temporary data (pending reservations)
5. **Use pipelines** for independent read operations
