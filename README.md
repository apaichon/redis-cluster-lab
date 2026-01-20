# Redis Cluster Scaling Lab

A hands-on lab demonstrating Redis Cluster scaling with a ticket reservation system using Go and Docker Compose.

## Overview

This lab teaches you how to:
- Set up and manage a Redis Cluster
- Build cluster-aware applications with Go
- Scale clusters horizontally (add/remove nodes)
- Handle automatic failover
- **Understand hash slot distribution and sharding**
- **Design key structures for optimal data distribution**
- **Use hash tags for co-located data**
- **Handle cross-slot operations and limitations**
- Implement atomic operations with Lua scripts
- **Perform manual resharding and rebalancing**

## Prerequisites

- Docker and Docker Compose
- Go 1.21+
- Basic understanding of Redis

## Quick Start

```bash
# 1. Start the cluster
make start

# 2. Build the application
make build

# 3. Run the demo
make demo
```

## Lab Exercises

### Exercise 1: Understanding the Cluster

Start the cluster and explore its structure:

```bash
# Start 6-node cluster (3 masters + 3 replicas)
make start

# View cluster status
make cluster-info
```

**Observe:**
- 3 master nodes, each handling ~5461 hash slots
- 3 replica nodes, one for each master
- Total of 16384 hash slots distributed across masters

### Exercise 2: Create Events and Make Reservations

```bash
# Create an event
./app/ticket-reservation create-event \
  --name "Tech Conference 2024" \
  --rows 10 \
  --seats 20 \
  --price 150

# Note the event ID and slot assignment
# Example output: Event data stored on slot 12345 (Node: localhost:7003)

# View seat map
./app/ticket-reservation seat-map <event-id>

# Reserve seats
./app/ticket-reservation reserve \
  --event <event-id> \
  --user alice \
  --seats A1,A2,A3 \
  --name "Alice Smith" \
  --email "alice@example.com"

# Check availability
./app/ticket-reservation availability <event-id>

# Confirm reservation
./app/ticket-reservation confirm <reservation-id>
```

**Key Concept: Hash Tags**

Notice how related keys use hash tags:
- `{event:abc123}` - Event metadata
- `{event:abc123}:seats` - Seat status hash
- `{event:abc123}:stats` - Event statistics

The `{event:abc123}` portion ensures all event data lands in the same hash slot, enabling atomic Lua scripts across related keys.

### Exercise 3: Scale Up - Add a Master Node

```bash
# Current state: 3 masters
make cluster-info

# Add redis-7 as a new master
make scale-up

# Observe:
# 1. New node joins as empty master
# 2. Slots are rebalanced (~4096 slots move to new node)
# 3. Cluster now has 4 masters

make cluster-info
```

**What Happens During Rebalancing:**
- Redis redistributes hash slots from existing masters
- Keys are migrated to new node as slots move
- Operations continue during migration (MOVED/ASK redirects)
- No downtime!

### Exercise 4: Scale Up - Add a Replica

```bash
# Add redis-8 as replica of redis-7
make scale-add-replica

# Verify replication
make cluster-info

# The new master now has redundancy
```

### Exercise 5: Automatic Failover

Test Redis Cluster's automatic failover:

```bash
# Watch cluster state in one terminal
make watch-cluster

# In another terminal, trigger failover
make failover

# Observe:
# 1. Master (redis-1) is stopped
# 2. After ~5 seconds (cluster-node-timeout), replica detects failure
# 3. Replica promotes itself to master
# 4. Cluster continues operating!

# Recover the failed node
make recover

# It rejoins as a replica of the promoted node
```

### Exercise 6: Scale Down - Remove a Node

```bash
# Remove the node we added (redis-7)
make scale-down

# For a master, this:
# 1. Reshards slots to remaining masters
# 2. Removes the empty node
# 3. Stops the container
```

### Exercise 7: Load Testing

Test the system under concurrent load:

```bash
# Create a large event
./app/ticket-reservation create-event \
  --name "Load Test Concert" \
  --rows 50 \
  --seats 100 \
  --price 25

# Run load test with 100 concurrent users
make load-test USERS=100 SEATS=2

# Observe:
# - Successful vs failed reservations
# - Throughput (reservations/second)
# - Conflict detection (same seat attempts)
```

### Exercise 8: Observe Data Distribution

```bash
# Create multiple events
./app/ticket-reservation create-event --name "Event A"
./app/ticket-reservation create-event --name "Event B"
./app/ticket-reservation create-event --name "Event C"

# Each event ID maps to different hash slots
# This distributes load across the cluster

# Check which node handles each event
./app/ticket-reservation cluster-info
```

---

## Sharding Labs

Redis Cluster uses hash-based sharding to distribute data across nodes. These labs help you understand how sharding works and how to design for it.

### Sharding Lab 1: Understanding Slot Distribution

Learn how Redis distributes the 16384 hash slots across master nodes:

```bash
# View detailed slot distribution
make slot-info

# Output shows:
# - Each master's slot range (e.g., 0-5460, 5461-10922, 10923-16383)
# - Percentage of slots per node
# - Visual bar chart of distribution
```

**Key Concepts:**
- Redis Cluster divides keyspace into 16384 slots
- Each master is responsible for a subset of slots
- Keys are assigned to slots using: `CRC16(key) mod 16384`

### Sharding Lab 2: Key-to-Slot Mapping

Explore how keys map to slots and nodes:

```bash
# Check where specific keys would be stored
make key-slot KEY=user:1001
make key-slot KEY=user:1002
make key-slot KEY=order:5001

# Check multiple keys at once
./app/ticket-reservation key-slot user:1 user:2 product:abc order:xyz
```

**Observe:**
- Different keys hash to different slots
- Keys are distributed across multiple nodes
- Slot assignment is deterministic (same key always maps to same slot)

### Sharding Lab 3: Hash Tags Deep Dive

Understand how hash tags control data placement:

```bash
# Run the hash tag demonstration
make hash-tag-demo

# This shows:
# 1. Keys WITHOUT hash tags → distributed across nodes
# 2. Keys WITH same hash tag → all on same slot
# 3. Real-world pattern for our reservation system
```

**Hash Tag Syntax:**
```
{user:1001}:profile    → hashes on "user:1001"
{user:1001}:cart       → hashes on "user:1001" (SAME slot!)
{user:1001}:orders     → hashes on "user:1001" (SAME slot!)
```

**When to Use Hash Tags:**
- Related data that needs atomic operations (Lua scripts)
- Data that's frequently accessed together
- Transactions across multiple keys

**When NOT to Use Hash Tags:**
- Unrelated data (wastes slot capacity)
- High-volume keys (creates hot spots)
- When even distribution is more important

### Sharding Lab 4: Cross-Slot Operations

Learn what operations work across slots and what doesn't:

```bash
# Run the cross-slot demonstration
make cross-slot-demo

# This demonstrates:
# 1. MGET on keys in different slots (client handles routing)
# 2. MGET on keys in same slot (single node operation)
# 3. Lua script limitations (MUST be same slot)
```

**Cross-Slot Rules:**
| Operation | Cross-Slot Support |
|-----------|-------------------|
| Single-key commands | Always works (client routes) |
| MGET/MSET | go-redis handles automatically |
| MULTI/EXEC | Same slot only |
| Lua scripts | Same slot only |
| WATCH | Same slot only |

### Sharding Lab 5: Comprehensive Sharding Demo

Run through all sharding concepts in one demonstration:

```bash
make sharding-demo

# This covers:
# 1. CRC16 hashing algorithm
# 2. Data distribution patterns
# 3. Sequential key problems
# 4. Hash tags for transactions
# 5. Atomic operations with Lua
```

### Sharding Lab 6: Analyzing Key Distribution

Analyze how your actual data is distributed:

```bash
# Analyze all keys
make analyze-distribution

# Analyze specific pattern
make analyze-distribution PATTERN="event:*" LIMIT=500

# This shows:
# - Keys per node (should be roughly equal)
# - Hot spots (slots with many keys)
# - Sample keys and their slots
```

**Healthy Distribution Indicators:**
- Each node has ~33% of keys (for 3 masters)
- No single slot has disproportionate keys
- No single node is overloaded

### Sharding Lab 7: Manual Resharding

Learn to manually move slots between nodes:

```bash
# First, understand the resharding process
make reshard-demo

# View current masters and their IDs
make cluster-info

# Move 500 slots from one master to another
make reshard-slots FROM=<node-id-1> TO=<node-id-2> SLOTS=500

# Verify new distribution
make slot-info
```

**Resharding Process:**
1. Target node marks slots as IMPORTING
2. Source node marks slots as MIGRATING
3. Keys are migrated one by one
4. Cluster updates slot ownership
5. Clients receive MOVED redirects

### Sharding Lab 8: Key Migration During Resharding

Understand what happens to client requests during migration:

```bash
make migration-demo

# This explains:
# - IMPORTING/MIGRATING states
# - ASK vs MOVED redirects
# - How clients handle redirects
# - Why there's no downtime
```

**Redirect Types:**
- **ASK**: Temporary redirect during migration (don't cache)
- **MOVED**: Permanent redirect (update slot→node cache)

### Sharding Lab 9: Hot Key Simulation

Understand and mitigate hot key problems:

```bash
# Simulate heavy traffic to a single key
make hotkey-demo DURATION=5

# This demonstrates:
# - All traffic goes to one node
# - Performance impact
# - Mitigation strategies
```

**Hot Key Solutions:**
1. **Read Replicas**: Use `READONLY` mode for replicas
2. **Local Caching**: Cache hot data in application memory
3. **Key Splitting**: `{product:hot}:shard:1`, `{product:hot}:shard:2`
4. **Client-Side Caching**: Redis 6.0+ RESP3 protocol

### Sharding Lab 10: Design Patterns for Sharding

Apply sharding best practices:

```bash
# Create events and observe distribution
./app/ticket-reservation create-event --name "Concert A"
./app/ticket-reservation create-event --name "Concert B"
./app/ticket-reservation create-event --name "Concert C"

# Check distribution
make analyze-distribution PATTERN="{event:*"
```

**Design Patterns:**

1. **Entity-Based Hash Tags** (our approach):
   ```
   {event:abc}           # event metadata
   {event:abc}:seats     # seat status
   {event:abc}:stats     # statistics
   ```
   Pros: Atomic operations, related data together
   Cons: Large events create hot spots

2. **UUID-Based Keys** (natural distribution):
   ```
   user:550e8400-e29b-41d4-a716-446655440000
   order:7c9e6679-7425-40de-944b-e07fc1f90ae7
   ```
   Pros: Even distribution
   Cons: Need external index for lookups

3. **Tenant-Based Sharding**:
   ```
   {tenant:acme}:users
   {tenant:acme}:products
   ```
   Pros: Tenant isolation, easy multi-tenancy
   Cons: Large tenants create hot spots

---

## Architecture

### Ticket Reservation System

```
┌─────────────────────────────────────────────────────────────┐
│                    Ticket Reservation CLI                    │
├─────────────────────────────────────────────────────────────┤
│  Commands: create-event, reserve, confirm, cancel, etc.     │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    Reservation Service                       │
│  - Atomic seat reservation (Lua scripts)                    │
│  - Conflict detection                                        │
│  - Waitlist management                                       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    Redis Cluster Client                      │
│  - Automatic routing (MOVED/ASK handling)                   │
│  - Connection pooling                                        │
│  - Retry logic                                               │
└─────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌─────────┐     ┌─────────┐     ┌─────────┐
        │ Master 1│     │ Master 2│     │ Master 3│
        │ 0-5460  │     │5461-10922│    │10923-16383│
        └────┬────┘     └────┬────┘     └────┬────┘
             │               │               │
        ┌────┴────┐     ┌────┴────┐     ┌────┴────┐
        │Replica 1│     │Replica 2│     │Replica 3│
        └─────────┘     └─────────┘     └─────────┘
```

### Key Patterns Used

1. **Hash Tags for Co-location**
   ```
   {event:123}          → Event metadata
   {event:123}:seats    → Seat availability
   {event:123}:stats    → Statistics
   ```
   All keys with `{event:123}` hash to the same slot.

2. **Lua Scripts for Atomicity**
   ```lua
   -- Check all seats available
   for each seat do
       if status != 'available' then return error
   end
   -- Reserve all seats
   for each seat do
       HSET seats_key seat_id 'pending'
   end
   ```

3. **Sorted Sets for Waitlist (FIFO)**
   ```
   ZADD {event:123}:waitlist <timestamp> <user_data>
   ```

## Troubleshooting

### Cluster Won't Initialize
```bash
# Check all containers are running
docker compose ps

# Check logs
docker logs redis-1

# Manually ping nodes
docker exec redis-1 redis-cli -p 7001 ping
```

### Application Can't Connect
```bash
# Verify cluster state
docker exec redis-1 redis-cli -p 7001 cluster info

# Check from host
redis-cli -c -p 7001 cluster nodes
```

### Slots Not Balanced
```bash
# Force rebalance
docker exec redis-1 redis-cli --cluster rebalance localhost:7001
```

## Files Structure

```
cluster-labs/
├── docker-compose.yml      # Redis cluster (8 nodes)
├── redis.conf              # Redis configuration
├── Makefile                # Lab commands
├── app/
│   ├── main.go             # CLI entry point
│   ├── go.mod
│   ├── cluster/
│   │   └── client.go       # Cluster client wrapper
│   ├── models/
│   │   └── models.go       # Data models
│   ├── service/
│   │   └── reservation.go  # Business logic + Lua scripts
│   └── cmd/
│       ├── commands.go     # CLI command handlers
│       └── sharding.go     # Sharding lab commands
├── scripts/
│   ├── init-cluster.sh     # Cluster initialization
│   ├── scale-add-master.sh # Add master node
│   ├── scale-add-replica.sh# Add replica
│   ├── scale-remove-node.sh# Remove node
│   ├── failover-test.sh    # Test failover
│   └── load-test.sh        # Load testing
└── README.md
```

## Documentation

For in-depth learning, see these detailed guides:

| Document | Description |
|----------|-------------|
| [Cluster Fundamentals](docs/REDIS-CLUSTER-FUNDAMENTALS.md) | Complete guide covering architecture, data flow, code structure, and PostgreSQL integration |
| [Monitoring Guide](docs/CLUSTER-MONITORING-GUIDE.md) | Quick reference for checking data location and cluster health |

## Key Takeaways

### Cluster Fundamentals
1. **16384 Hash Slots** - keyspace divided into slots, distributed across masters
2. **CRC16 Hashing** - `slot = CRC16(key) mod 16384` determines key placement
3. **Automatic Failover** - replicas promote when masters fail (~5 second detection)
4. **Cluster-aware clients** - handle MOVED/ASK redirections automatically

### Sharding Best Practices
5. **Hash Tags** - `{tag}:suffix` ensures related keys land on same slot
6. **Cross-slot limitations** - Lua scripts/MULTI only work within single slot
7. **Key design matters** - plan key structure for distribution AND atomicity
8. **Avoid hot spots** - distribute load evenly, watch for popular keys

### Scaling Operations
9. **Horizontal Scaling** - add masters to increase capacity
10. **Rebalancing** - redistributes slots with zero downtime
11. **Live Migration** - keys move seamlessly during resharding
12. **Lua Scripts** - provide atomicity for multi-key operations (same slot)
