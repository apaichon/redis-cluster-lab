# Part 4: Workflows & Operations

## Overview

This part covers the detailed workflows for event creation, seat reservation, and automatic failover in Redis Cluster.

---

## 1. Event Creation Workflow

Creating an event involves initializing multiple related keys atomically.

### Step-by-Step Process

```
┌─────────────────────────────────────────────────────────────────┐
│                    CREATE EVENT WORKFLOW                         │
└─────────────────────────────────────────────────────────────────┘

Step 1: Generate Event ID
        └── event_id = uuid.New().String()[:8]
        └── Example: "abc12345"

Step 2: Calculate Hash Slot
        └── slot = CRC16("{event:abc12345}") mod 16384
        └── Example: slot 7186 → Node 2

Step 3: Create Event Hash
        └── HSET {event:abc123}
              id "abc123"
              name "Concert"
              venue "Main Hall"
              total_seats 100
              price 50.00
              created_at "2024-01-15T10:00:00Z"

Step 4: Initialize Seat Map
        └── HSET {event:abc123}:seats
              A1 "available"
              A2 "available"
              ...
              J10 "available"

Step 5: Initialize Statistics
        └── HSET {event:abc123}:stats
              available 100
              pending 0
              sold 0

Step 6: Initialize Empty Waitlist
        └── (Sorted set created on first entry)
```

### Data State After Creation

```
Node 2 (slots 5461-10922, including slot 7186):
├── {event:abc123}
│   ├── id: "abc123"
│   ├── name: "Concert"
│   ├── venue: "Main Hall"
│   └── total_seats: 100
│
├── {event:abc123}:seats
│   ├── A1: "available"
│   ├── A2: "available"
│   └── ... (100 seats)
│
└── {event:abc123}:stats
    ├── available: 100
    ├── pending: 0
    └── sold: 0
```

---

## 2. Reservation Workflow

The reservation process handles concurrent users trying to book the same seats.

### Complete Reservation Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                   RESERVATION WORKFLOW                           │
└─────────────────────────────────────────────────────────────────┘

                    ┌───────────────┐
                    │ User Request  │
                    │ Seats: A1,A2  │
                    └───────┬───────┘
                            │
                            ▼
                    ┌───────────────┐
                    │ Validate      │
                    │ Event Exists  │
                    └───────┬───────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │    Execute Lua Script   │
              │    (Atomic Operation)   │
              └─────────────┬───────────┘
                            │
              ┌─────────────┴─────────────┐
              │                           │
              ▼                           ▼
    ┌─────────────────┐         ┌─────────────────┐
    │  Seats Available │         │ Seats NOT Avail │
    │                  │         │                 │
    │ 1. Mark pending  │         │ Return error    │
    │ 2. Create res    │         │ or join waitlist│
    │ 3. Set TTL       │         │                 │
    └────────┬─────────┘         └─────────────────┘
             │
             ▼
    ┌─────────────────┐
    │ Return          │
    │ Reservation ID  │
    │ (15 min TTL)    │
    └────────┬────────┘
             │
    ┌────────┴────────┐
    │                 │
    ▼                 ▼
┌─────────────┐  ┌─────────────┐
│ User Pays   │  │ TTL Expires │
│ (Confirm)   │  │ (Timeout)   │
└──────┬──────┘  └──────┬──────┘
       │                │
       ▼                ▼
┌─────────────┐  ┌─────────────┐
│ Mark SOLD   │  │ Release     │
│ Update stats│  │ Seats back  │
│             │  │ to available│
└─────────────┘  └─────────────┘
```

### Seat Status Transitions

```
available → pending:resXXX → sold
     ↑                        │
     └────────────────────────┘
         (cancellation or TTL expiry)
```

### Handling Concurrent Requests

When multiple users try to reserve the same seat simultaneously:

```
User A: Reserve seat A1          User B: Reserve seat A1
    │                                │
    ▼                                ▼
┌─────────────────────────────────────────────────┐
│           Lua Script Execution                   │
│  (Only ONE executes at a time - atomic)         │
├─────────────────────────────────────────────────┤
│                                                 │
│  User A's script runs first:                    │
│  1. Check A1 → "available" ✓                    │
│  2. Set A1 → "pending:resA"                     │
│                                                 │
│  User B's script runs second:                   │
│  1. Check A1 → "pending:resA" ✗                 │
│  2. Return error: "Seat not available"          │
│                                                 │
└─────────────────────────────────────────────────┘
```

---

## 3. Confirmation Workflow

After payment, the reservation is confirmed.

### Confirm Process

```
Step 1: Validate reservation exists and is pending
        └── GET reservation:resXXX
        └── Check status = "pending"

Step 2: Update seat status (atomic via Lua)
        └── For each seat:
            HSET {event:abc123}:seats A1 "sold:resXXX"

Step 3: Update statistics
        └── HINCRBY {event:abc123}:stats pending -2
        └── HINCRBY {event:abc123}:stats sold 2

Step 4: Remove TTL (reservation is permanent)
        └── PERSIST reservation:resXXX

Step 5: Update reservation status
        └── HSET reservation:resXXX status "confirmed"
```

---

## 4. Cancellation / Expiry Workflow

When a reservation is cancelled or expires:

```
┌─────────────────────────────────────────────────────────────────┐
│                   CANCELLATION WORKFLOW                          │
└─────────────────────────────────────────────────────────────────┘

1. Mark seats as available again
   └── HSET {event:abc123}:seats A1 "available"
   └── HSET {event:abc123}:seats A2 "available"

2. Update statistics
   └── HINCRBY {event:abc123}:stats pending -2
   └── HINCRBY {event:abc123}:stats available 2

3. Delete reservation record
   └── DEL reservation:resXXX

4. Check waitlist
   └── ZRANGE {event:abc123}:waitlist 0 0
   └── If user waiting, notify them

5. Process waitlist (optional automatic)
   └── Create new pending reservation
   └── Send notification to waiting user
```

---

## 5. Automatic Failover Workflow

Redis Cluster automatically handles master node failures.

### Normal Operation State

```
Time 0s: Normal Operation

┌─────────┐     ┌─────────┐     ┌─────────┐
│Master 1 │     │Master 2 │     │Master 3 │
│  7001   │     │  7002   │     │  7003   │
│Slots    │     │Slots    │     │Slots    │
│0-5460   │     │5461-    │     │10923-   │
│         │     │10922    │     │16383    │
└────┬────┘     └────┬────┘     └────┬────┘
     │               │               │
┌────▼────┐     ┌────▼────┐     ┌────▼────┐
│Replica 4│     │Replica 5│     │Replica 6│
│  7004   │     │  7005   │     │  7006   │
└─────────┘     └─────────┘     └─────────┘
```

### Failure Detection Phase

```
Time 1s: Master 1 Fails

┌─────────┐     ┌─────────┐     ┌─────────┐
│Master 1 │     │Master 2 │     │Master 3 │
│  FAIL   │     │  7002   │     │  7003   │
│  ✗✗✗    │     │         │     │         │
└────┬────┘     └────┬────┘     └────┬────┘
     │ ✗             │               │
┌────▼────┐     ┌────▼────┐     ┌────▼────┐
│Replica 4│     │Replica 5│     │Replica 6│
│  7004   │     │  7005   │     │  7006   │
│(waiting)│     │         │     │         │
└─────────┘     └─────────┘     └─────────┘

Time 2-5s: Detection
- Replica 4 notices Master 1 not responding
- Other masters also report Master 1 unreachable
- Gossip protocol spreads PFAIL status
```

### Failover Execution Phase

```
Time 5s: Failure Confirmed (cluster-node-timeout reached)

- Majority of masters agree: Master 1 is FAIL
- Replica 4 starts election process

Time 6s: Replica Promotion

┌─────────┐     ┌─────────┐     ┌─────────┐
│ (down)  │     │Master 2 │     │Master 3 │
│         │     │  7002   │     │  7003   │
└─────────┘     └────┬────┘     └────┬────┘
                     │               │
┌─────────┐     ┌────▼────┐     ┌────▼────┐
│*Master 4│     │Replica 5│     │Replica 6│
│  7004   │     │  7005   │     │  7006   │
│(PROMOTED)     │         │     │         │
│Slots 0-5460   │         │     │         │
└─────────┘     └─────────┘     └─────────┘

- Replica 4 becomes Master 4
- Takes over slots 0-5460
- Clients automatically redirect to new master
```

### Recovery Phase

```
Time Later: Old Master Recovers

┌─────────┐     ┌─────────┐     ┌─────────┐
│*Replica1│     │Master 2 │     │Master 3 │
│  7001   │     │  7002   │     │  7003   │
│(rejoins │     │         │     │         │
│as replica)    │         │     │         │
└────┬────┘     └────┬────┘     └────┬────┘
     │               │               │
┌────▼────┐     ┌────▼────┐     ┌────▼────┐
│ Master 4│     │Replica 5│     │Replica 6│
│  7004   │     │  7005   │     │  7006   │
└─────────┘     └─────────┘     └─────────┘

- Old Master 1 (7001) rejoins as replica of Master 4
- Data syncs from Master 4 to Replica 1
- Cluster returns to full redundancy
```

---

## 6. Key Timing Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| cluster-node-timeout | 5000ms | Time before node considered failed |
| replica-validity-factor | 10 | Max replication lag for promotion |
| failover-timeout | 30000ms | Max time for failover process |

### Failover Timeline

```
Event                    Time (typical)
─────────────────────────────────────────
Master stops responding   0ms
Replica detects failure   1000-2000ms
PFAIL status propagates   2000-3000ms
FAIL consensus reached    5000ms
Election initiated        5100ms
Replica promoted          5500ms
Clients redirected        6000ms
─────────────────────────────────────────
Total downtime            ~6 seconds
```

---

## 7. Client Behavior During Failover

### What Clients Experience

```
Before Failover:
Client → Master 1 (7001): GET {event:123}
Master 1 → Client: "Concert data"

During Failover (5-6 seconds):
Client → Master 1 (7001): GET {event:123}
Connection refused or timeout
Client retries...

After Failover:
Client → Master 1 (7001): GET {event:123}
Master 1 → Client: MOVED 7186 172.30.0.14:7004
Client → Master 4 (7004): GET {event:123}
Master 4 → Client: "Concert data"
```

### Smart Client Handling

Go-redis and other smart clients:
1. Maintain slot-to-node mapping cache
2. Automatically follow MOVED redirections
3. Update internal routing table
4. Retry failed operations

---

## 8. Workflow Best Practices

### For Reservations
- Always use Lua scripts for atomic operations
- Set appropriate TTLs for pending reservations
- Handle waitlist notifications asynchronously
- Log all state transitions for debugging

### For Failover
- Set cluster-node-timeout based on network conditions
- Monitor replication lag
- Test failover scenarios in staging
- Have alerting on cluster state changes

### For Recovery
- Old masters automatically rejoin as replicas
- Manual promotion can be done if needed
- Regular backups complement cluster redundancy
