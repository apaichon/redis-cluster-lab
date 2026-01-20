# Part 3: Code Structure & Implementation

## Overview

This part explains the project structure, key code components, and how data flows through the application when interacting with Redis Cluster.

---

## 1. Project Structure

```
cluster-labs/
├── docker-compose.yml      # Redis cluster infrastructure
├── redis.conf              # Redis configuration
├── Makefile                # Lab commands
│
├── app/
│   ├── main.go             # CLI entry point & command routing
│   ├── go.mod              # Go module definition
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
│       ├── create_event.go    # Create events
│       ├── reserve.go         # Make reservations
│       ├── release.go         # Cancel reservations
│       ├── availability.go    # Check seat availability
│       ├── waitlist.go        # Waitlist operations
│       └── demo.go            # Full demonstration
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
- Address mapping for Docker environments
- Connection pool management
- Cluster information retrieval
- Health checking

### models/models.go
**Purpose**: Define data structures used throughout the application

Key structures:
- Event: Event metadata
- Seat: Seat status information
- Reservation: Booking details
- WaitlistEntry: Waitlist queue item

### service/reservation.go
**Purpose**: Core business logic for reservations

Key features:
- Lua scripts for atomic operations
- Reservation workflow management
- Waitlist processing
- TTL handling

### cmd/*.go
**Purpose**: CLI command implementations

Each file handles a specific command:
- create_event.go: Initialize new events
- reserve.go: Book seats
- release.go: Cancel bookings
- availability.go: Check open seats

---

## 3. Cluster Client Implementation

### Address Mapping for Docker

The core challenge: Redis nodes announce Docker internal IPs.

```go
// cluster/client.go

// Maps internal Docker IPs to localhost
var addressMapper = map[string]string{
    "172.30.0.11": "127.0.0.1",
    "172.30.0.12": "127.0.0.1",
    "172.30.0.13": "127.0.0.1",
    "172.30.0.14": "127.0.0.1",
    "172.30.0.15": "127.0.0.1",
    "172.30.0.16": "127.0.0.1",
}

// Remap Docker IP to localhost
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
// Create cluster client with custom dialer
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{
        "127.0.0.1:7001",
        "127.0.0.1:7002",
        "127.0.0.1:7003",
    },

    // Custom dialer intercepts all connections
    Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
        // Remap 172.30.0.11:7001 → 127.0.0.1:7001
        mappedAddr := remapAddress(addr)

        netDialer := &net.Dialer{
            Timeout:   5 * time.Second,
            KeepAlive: 5 * time.Minute,
        }
        return netDialer.DialContext(ctx, network, mappedAddr)
    },
})
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

var reserveSeatsScript = redis.NewScript(`
    -- KEYS[1] = {event:ID} (event info)
    -- KEYS[2] = {event:ID}:seats (seat map)
    -- KEYS[3] = reservation ID
    -- ARGV = list of seats to reserve

    -- Step 1: Check all seats are available
    for i, seat in ipairs(ARGV) do
        local status = redis.call('HGET', KEYS[2], seat)
        if status ~= 'available' then
            return {err = 'Seat ' .. seat .. ' not available: ' .. (status or 'nil')}
        end
    end

    -- Step 2: All available - reserve atomically
    local reservation_id = KEYS[3]
    for i, seat in ipairs(ARGV) do
        redis.call('HSET', KEYS[2], seat, 'pending:' .. reservation_id)
    end

    -- Step 3: Update statistics
    local seat_count = #ARGV
    redis.call('HINCRBY', KEYS[1] .. ':stats', 'available', -seat_count)
    redis.call('HINCRBY', KEYS[1] .. ':stats', 'pending', seat_count)

    return {ok = 'reserved', count = seat_count}
`)
```

### Executing the Script

```go
func (s *ReservationService) ReserveSeats(ctx context.Context, eventID string, seats []string) (*Reservation, error) {
    reservationID := uuid.New().String()

    // All keys use same hash tag for co-location
    eventKey := fmt.Sprintf("{event:%s}", eventID)
    seatsKey := fmt.Sprintf("{event:%s}:seats", eventID)

    // Execute Lua script
    result, err := reserveSeatsScript.Run(ctx, s.client,
        []string{eventKey, seatsKey, reservationID},  // KEYS
        seats...,                                       // ARGV (seats)
    ).Result()

    if err != nil {
        return nil, fmt.Errorf("reservation failed: %w", err)
    }

    // Handle script response
    // ...

    return &Reservation{
        ID:      reservationID,
        EventID: eventID,
        Seats:   seats,
        Status:  "pending",
    }, nil
}
```

---

## 5. Data Flow Diagram

### Complete Reservation Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                    RESERVATION FLOW                               │
└──────────────────────────────────────────────────────────────────┘

User Request                     Application                    Redis Cluster
     │                               │                               │
     │  reserve A1,A2,A3            │                               │
     │  for event:abc123            │                               │
     │ ─────────────────────────────►                               │
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
     │                               │       KEYS[1]: {event:abc123} │
     │                               │       KEYS[2]: {event:abc123}:seats
     │                               │       ARGV: [A1, A2, A3]      │
     │                               │                               │
     │                               │                    ┌──────────┤
     │                               │                    │ Lua runs │
     │                               │                    │ atomically│
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
     │  Reservation confirmed       │                               │
     │ ◄─────────────────────────────                               │
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
| 6 | Lua Script | Marks seats as "pending:resID" |
| 7 | Lua Script | Updates availability statistics |
| 8 | Redis Node | Returns success response |
| 9 | Application | Creates reservation record |
| 10 | User | Receives confirmation |

---

## 6. Key Code Patterns

### Pattern 1: Hash Tag for Key Generation

```go
// Always use consistent hash tag format
func eventKey(eventID string) string {
    return fmt.Sprintf("{event:%s}", eventID)
}

func seatsKey(eventID string) string {
    return fmt.Sprintf("{event:%s}:seats", eventID)
}

func statsKey(eventID string) string {
    return fmt.Sprintf("{event:%s}:stats", eventID)
}

func waitlistKey(eventID string) string {
    return fmt.Sprintf("{event:%s}:waitlist", eventID)
}
```

### Pattern 2: TTL for Pending Reservations

```go
// Set expiration on pending reservation
func (s *Service) setReservationTTL(ctx context.Context, resID string) error {
    key := fmt.Sprintf("reservation:%s", resID)
    return s.client.Expire(ctx, key, 15*time.Minute).Err()
}
```

### Pattern 3: Pipeline for Multiple Independent Operations

```go
// Use pipeline when operations are independent
func (s *Service) GetEventStats(ctx context.Context, eventID string) (*Stats, error) {
    pipe := s.client.Pipeline()

    // Queue multiple commands
    availableCmd := pipe.HGet(ctx, statsKey(eventID), "available")
    pendingCmd := pipe.HGet(ctx, statsKey(eventID), "pending")
    soldCmd := pipe.HGet(ctx, statsKey(eventID), "sold")

    // Execute all at once
    _, err := pipe.Exec(ctx)
    if err != nil {
        return nil, err
    }

    return &Stats{
        Available: availableCmd.Val(),
        Pending:   pendingCmd.Val(),
        Sold:      soldCmd.Val(),
    }, nil
}
```

### Pattern 4: Error Handling from Lua Scripts

```go
// Lua scripts return structured errors
func handleLuaResult(result interface{}) error {
    switch v := result.(type) {
    case []interface{}:
        if len(v) >= 2 && v[0] == "err" {
            return fmt.Errorf("script error: %v", v[1])
        }
        return nil
    case string:
        if v == "OK" {
            return nil
        }
        return fmt.Errorf("unexpected result: %s", v)
    default:
        return fmt.Errorf("unknown result type: %T", result)
    }
}
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
