# Part 2: Architecture & Design Patterns

## Overview

This part covers the practical architecture of deploying Redis Cluster in Docker environments, addressing common challenges like network mapping, and designing data models for distributed systems.

---

## 1. Lab Architecture Overview

This lab runs Redis Cluster in Docker containers while the application runs on the host machine (macOS/Linux/Windows).

### Complete System Layout

```
┌─────────────────────────────────────────────────────────────────┐
│                        HOST MACHINE (macOS)                      │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                   Go Application                          │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐       │   │
│  │  │   CLI       │  │  Service    │  │  Cluster    │       │   │
│  │  │  Commands   │──│  Layer      │──│  Client     │       │   │
│  │  └─────────────┘  └─────────────┘  └──────┬──────┘       │   │
│  └───────────────────────────────────────────┼──────────────┘   │
│                                              │                   │
│                              127.0.0.1:7001-7006                 │
│                                              │                   │
│  ┌───────────────────────────────────────────┼──────────────┐   │
│  │              Docker Network (172.30.0.0/24)│              │   │
│  │                                           ▼              │   │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐                     │   │
│  │  │Redis-1  │ │Redis-2  │ │Redis-3  │  Masters            │   │
│  │  │.11:7001 │ │.12:7002 │ │.13:7003 │                     │   │
│  │  └────┬────┘ └────┬────┘ └────┬────┘                     │   │
│  │       │           │           │                          │   │
│  │  ┌────▼────┐ ┌────▼────┐ ┌────▼────┐                     │   │
│  │  │Redis-4  │ │Redis-5  │ │Redis-6  │  Replicas           │   │
│  │  │.14:7004 │ │.15:7005 │ │.16:7006 │                     │   │
│  │  └─────────┘ └─────────┘ └─────────┘                     │   │
│  │                                                          │   │
│  │  ┌─────────┐ ┌─────────┐                                 │   │
│  │  │Redis-7  │ │Redis-8  │  Spare (for scaling)            │   │
│  │  │.17:7007 │ │.18:7008 │                                 │   │
│  │  └─────────┘ └─────────┘                                 │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Node Roles

| Node | Docker IP | Host Port | Role |
|------|-----------|-----------|------|
| Redis-1 | 172.30.0.11 | 7001 | Master (slots 0-5460) |
| Redis-2 | 172.30.0.12 | 7002 | Master (slots 5461-10922) |
| Redis-3 | 172.30.0.13 | 7003 | Master (slots 10923-16383) |
| Redis-4 | 172.30.0.14 | 7004 | Replica of Master 1 |
| Redis-5 | 172.30.0.15 | 7005 | Replica of Master 2 |
| Redis-6 | 172.30.0.16 | 7006 | Replica of Master 3 |
| Redis-7 | 172.30.0.17 | 7007 | Spare (for scaling) |
| Redis-8 | 172.30.0.18 | 7008 | Spare (for scaling) |

---

## 2. Docker Network Challenge

### The Problem

Redis Cluster nodes announce their IP addresses to clients. In Docker, nodes announce internal container IPs that the host cannot reach directly.

```
Cluster Node Announcement:
"I am available at 172.30.0.11:7001"

Host Application:
"I cannot connect to 172.30.0.11!" (Docker internal network)
```

### The Solution: Address Mapping

Create a custom dialer that remaps Docker internal IPs to localhost.

```
172.30.0.11:7001 ──► 127.0.0.1:7001 (via Docker port mapping)
172.30.0.12:7002 ──► 127.0.0.1:7002
172.30.0.13:7003 ──► 127.0.0.1:7003
```

### Implementation Pattern

```go
// Address mapper configuration
var addressMapper = map[string]string{
    "172.30.0.11": "127.0.0.1",
    "172.30.0.12": "127.0.0.1",
    "172.30.0.13": "127.0.0.1",
    "172.30.0.14": "127.0.0.1",
    "172.30.0.15": "127.0.0.1",
    "172.30.0.16": "127.0.0.1",
}

// Custom dialer function
func remapAddress(addr string) string {
    host, port, _ := net.SplitHostPort(addr)
    if mapped, ok := addressMapper[host]; ok {
        return net.JoinHostPort(mapped, port)
    }
    return addr
}
```

### Docker Compose Port Mapping

```yaml
services:
  redis-1:
    ports:
      - "7001:7001"   # Host:Container
      - "17001:17001" # Cluster bus port
    networks:
      redis-cluster:
        ipv4_address: 172.30.0.11
```

---

## 3. Data Model Design for Clustering

### Hash Tag Strategy

When designing data models for Redis Cluster, group related data using hash tags.

```
┌─────────────────────────────────────────────────────────────────┐
│                     EVENT DATA MODEL                             │
│                                                                  │
│  Hash Tag: {event:ID} ensures all related data on same node     │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ {event:abc123}           (Hash - Event metadata)         │    │
│  │   ├── id: "abc123"                                       │    │
│  │   ├── name: "Concert"                                    │    │
│  │   ├── venue: "Main Hall"                                 │    │
│  │   ├── total_seats: 100                                   │    │
│  │   └── price: 50.00                                       │    │
│  └─────────────────────────────────────────────────────────┘    │
│                           │                                      │
│                           ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ {event:abc123}:seats    (Hash - Seat status)             │    │
│  │   ├── A1: "available"                                    │    │
│  │   ├── A2: "pending:res123"                               │    │
│  │   ├── A3: "sold:res456"                                  │    │
│  │   └── ...                                                │    │
│  └─────────────────────────────────────────────────────────┘    │
│                           │                                      │
│                           ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ {event:abc123}:reservations (Set - Reservation IDs)      │    │
│  │   └── ["res123", "res456", "res789"]                     │    │
│  └─────────────────────────────────────────────────────────┘    │
│                           │                                      │
│                           ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ {event:abc123}:waitlist (Sorted Set - by timestamp)      │    │
│  │   └── [(user1, 1705123456), (user2, 1705123457)]         │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Key Naming Conventions

| Pattern | Example | Purpose |
|---------|---------|---------|
| `{entity:id}` | `{event:123}` | Main entity hash |
| `{entity:id}:attribute` | `{event:123}:seats` | Related data |
| `{entity:id}:collection` | `{event:123}:waitlist` | Lists/sets of related items |

### Benefits of This Design

1. **Atomic Operations**: All event data on one node enables transactions
2. **Lua Script Support**: Scripts can access all related keys
3. **Consistent Routing**: Application always talks to same node for one event
4. **Simplified Logic**: No cross-node coordination needed

---

## 4. Redis Data Structures for This Model

### Hash (HSET/HGET)
Used for: Event metadata, seat status

```redis
HSET {event:abc123} id "abc123" name "Concert" total_seats 100
HGET {event:abc123} name
→ "Concert"
```

### Hash for Seat Map
```redis
HSET {event:abc123}:seats A1 "available" A2 "available" A3 "sold"
HGET {event:abc123}:seats A1
→ "available"
```

### Set (SADD/SMEMBERS)
Used for: Reservation tracking

```redis
SADD {event:abc123}:reservations "res123" "res456"
SMEMBERS {event:abc123}:reservations
→ ["res123", "res456"]
```

### Sorted Set (ZADD/ZRANGE)
Used for: Waitlist with priority (timestamp)

```redis
ZADD {event:abc123}:waitlist 1705123456 "user1" 1705123457 "user2"
ZRANGE {event:abc123}:waitlist 0 -1 WITHSCORES
→ [("user1", 1705123456), ("user2", 1705123457)]
```

---

## 5. Designing for Cluster Operations

### Multi-Key Operations

Operations that work across keys must have keys on the same slot.

**Works (same hash tag):**
```redis
MGET {user:123}:name {user:123}:email {user:123}:phone
```

**Fails (different slots):**
```redis
MGET user:123:name user:456:name  ← CROSSSLOT error!
```

### Transaction Support

MULTI/EXEC only works with keys in the same slot.

**Works:**
```redis
MULTI
HINCRBY {event:123}:stats available -1
HINCRBY {event:123}:stats pending 1
HSET {event:123}:seats A1 "pending"
EXEC
```

### Lua Script Design

All keys accessed by a script must be passed in KEYS array and be on the same node.

```lua
-- Script to atomically check and reserve seats
-- KEYS[1] = {event:123}
-- KEYS[2] = {event:123}:seats
-- ARGV = seat list

local seats_key = KEYS[2]
for i, seat in ipairs(ARGV) do
    local status = redis.call('HGET', seats_key, seat)
    if status ~= 'available' then
        return {err = 'Seat not available'}
    end
end
-- All available, reserve them
for i, seat in ipairs(ARGV) do
    redis.call('HSET', seats_key, seat, 'pending')
end
return {ok = 'reserved'}
```

---

## 6. Architecture Best Practices

### DO: Use Hash Tags for Related Data
```
{user:123}:profile
{user:123}:sessions
{user:123}:orders
```

### DON'T: Store Unrelated Data with Same Hash Tag
```
{global}:user:123    ← All users on one node!
{global}:user:456    ← Creates hotspot
```

### DO: Design for Node Independence
Each event should be completely independent, allowing different events to be on different nodes.

### DON'T: Create Cross-Event Dependencies
```
{all_events}:seat_count  ← Requires all events on one node
```

### DO: Use TTL for Temporary Data
```redis
SET {event:123}:lock:A1 "res456" EX 900  # 15 minute lock
```

### DON'T: Forget to Clean Up
Design expiration into your data model from the start.

---

## 7. Summary

| Design Principle | Implementation |
|-----------------|----------------|
| **Data Co-location** | Use hash tags `{entity:id}` |
| **Atomic Operations** | Lua scripts with co-located keys |
| **Network Mapping** | Custom dialer for Docker IPs |
| **Data Structures** | Hash, Set, Sorted Set per use case |
| **TTL Management** | Built-in expiration for temporary data |
| **Independence** | Each entity fully self-contained |

### Key Questions When Designing

1. What operations need to be atomic?
2. Which keys need to be on the same node?
3. How will data expire or be cleaned up?
4. What's the access pattern (read-heavy, write-heavy)?
5. How will the data scale as the cluster grows?
