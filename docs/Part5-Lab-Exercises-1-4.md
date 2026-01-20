# Part 5: Lab Exercises (Labs 1-4)

## Overview

This part provides hands-on lab exercises covering cluster setup, hash slots, atomic operations, and scaling up.

---

## Lab 1: Cluster Setup & Basics

### Objective
Understand how to initialize a Redis Cluster and verify its basic configuration.

### Prerequisites
- Docker and Docker Compose installed
- Go 1.21 or later
- Terminal access

### Step 1: Start the Cluster

```bash
# Navigate to project directory
cd cluster-labs

# Start all Redis containers
make start
```

**What happens behind the scenes:**
1. Docker creates 6 Redis containers (redis-1 through redis-6)
2. Each container uses the shared redis.conf
3. Ports 7001-7006 are mapped to localhost

### Step 2: Initialize Cluster Topology

```bash
# Run cluster initialization script
make init-cluster
```

**The init script performs:**
```bash
redis-cli --cluster create \
    172.30.0.11:7001 172.30.0.12:7002 172.30.0.13:7003 \
    172.30.0.14:7004 172.30.0.15:7005 172.30.0.16:7006 \
    --cluster-replicas 1
```

This creates:
- 3 masters with slots distributed evenly
- 3 replicas (one per master)

### Step 3: Verify Cluster State

```bash
make cluster-info
```

**Expected Output:**
```
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_slots_pfail:0
cluster_slots_fail:0
cluster_known_nodes:6
cluster_size:3
```

### Step 4: Check Node Roles

```bash
make cluster-nodes
```

**Understanding the output:**
```
<node-id> 172.30.0.11:7001 master - 0 1705312345 1 connected 0-5460
<node-id> 172.30.0.14:7004 slave <master-id> 0 1705312345 4 connected
```

Fields explained:
- Node ID (40-character hex string)
- IP:Port
- Role (master/slave)
- Connected flags
- Slot range (for masters)

### Key Learning Points

1. **cluster_state:ok** means cluster is healthy
2. **16384 slots** must all be assigned
3. **Replica count** should match masters for HA
4. **Connected** status shows reachable nodes

---

## Lab 2: Hash Slots & Key Distribution

### Objective
Understand how keys map to specific nodes based on hash slots.

### Step 1: Check Slot for a Key

```bash
# Using redis-cli
redis-cli -c -p 7001 CLUSTER KEYSLOT "user:1001"
```

**Output:**
```
(integer) 12539
```

This means "user:1001" maps to slot 12539.

### Step 2: Find Which Node Owns a Slot

```bash
redis-cli -c -p 7001 CLUSTER SLOTS
```

Look for the range containing slot 12539.

### Step 3: Verify Key Location

```bash
# Set a key
redis-cli -c -p 7001 SET user:1001 "John Doe"

# Check where it landed
redis-cli -c -p 7001 DEBUG OBJECT user:1001
```

### Step 4: Hash Tag Demonstration

```bash
# Without hash tag (different slots)
redis-cli -c -p 7001 CLUSTER KEYSLOT "event:123"
redis-cli -c -p 7001 CLUSTER KEYSLOT "event:123:seats"
redis-cli -c -p 7001 CLUSTER KEYSLOT "event:123:waitlist"
```

Notice they go to DIFFERENT slots!

```bash
# With hash tag (same slot)
redis-cli -c -p 7001 CLUSTER KEYSLOT "{event:123}"
redis-cli -c -p 7001 CLUSTER KEYSLOT "{event:123}:seats"
redis-cli -c -p 7001 CLUSTER KEYSLOT "{event:123}:waitlist"
```

All return the SAME slot number!

### Step 5: Cross-Slot Error Demo

```bash
# This will fail
redis-cli -c -p 7001 MGET user:1 user:2 user:3
```

**Error:**
```
(error) CROSSSLOT Keys in request don't hash to the same slot
```

```bash
# This works (same hash tag)
redis-cli -c -p 7001 MSET "{user:1}:name" "Alice" "{user:1}:email" "alice@test.com"
redis-cli -c -p 7001 MGET "{user:1}:name" "{user:1}:email"
```

### Key Learning Points

1. **CRC16** hash determines slot (0-16383)
2. **Hash tags** `{...}` control slot placement
3. **Multi-key operations** require same slot
4. **CROSSSLOT errors** occur when keys are on different nodes

---

## Lab 3: Atomic Operations with Lua

### Objective
Understand why Lua scripts are essential for atomic multi-key operations.

### Step 1: Run the Demo Application

```bash
# Build the application
make build

# Run the demo
make demo
```

### Step 2: Observe Event Creation

The demo creates an event with 50 seats. Watch for:
```
Creating event: Tech Conference
Event created: evt-abc123
Seats initialized: 50
```

All related keys use hash tag `{event:abc123}`.

### Step 3: Watch Concurrent Reservations

The demo spawns multiple concurrent reservation requests:
```
User-1 reserving: A1, A2
User-2 reserving: A1, A3
User-3 reserving: B1, B2
```

### Step 4: Observe Atomic Behavior

Despite concurrent requests:
- Only ONE user gets seat A1
- Other users receive "not available" errors
- No double-booking occurs

**This is because the Lua script runs atomically:**
```lua
-- Check and set happen as one operation
for i, seat in ipairs(ARGV) do
    local status = redis.call('HGET', KEYS[2], seat)
    if status ~= 'available' then
        return {err = 'Seat not available'}
    end
end
-- If we reach here, all seats were available
for i, seat in ipairs(ARGV) do
    redis.call('HSET', KEYS[2], seat, 'pending')
end
```

### Step 5: Verify Final State

```bash
# Check seat status
redis-cli -c -p 7001 HGETALL "{event:abc123}:seats"
```

Each seat should be either:
- `available` - not reserved
- `pending:resXXX` - reserved but not paid
- `sold:resXXX` - confirmed

### Why Lua Instead of Transactions?

| Feature | MULTI/EXEC | Lua Script |
|---------|-----------|------------|
| Atomicity | ✓ | ✓ |
| Conditional logic | ✗ | ✓ |
| Cross-key reads during execution | ✗ | ✓ |
| Works in cluster | Only same slot | Only same slot |

### Key Learning Points

1. **Lua scripts** execute atomically on the server
2. **No race conditions** between check and update
3. **All keys must be on same node** (use hash tags)
4. **Script caching** improves performance (EVALSHA)

---

## Lab 4: Scaling Up (Add Master)

### Objective
Learn how to add a new master node and rebalance the cluster.

### Step 1: Check Current State

```bash
make cluster-info
```

Note the current:
- Number of masters (should be 3)
- Slot distribution (~5461 slots each)

### Step 2: Start New Node

```bash
# Start redis-7 container
docker compose up -d redis-7
```

### Step 3: Add Node to Cluster

```bash
# Add as new master (empty initially)
make scale-up
```

**Behind the scenes:**
```bash
redis-cli --cluster add-node \
    172.30.0.17:7007 \
    172.30.0.11:7001
```

### Step 4: Verify New Node Joined

```bash
make cluster-nodes
```

You'll see redis-7 as a master with NO slots:
```
<node-id> 172.30.0.17:7007 master - 0 1705312345 7 connected
```

### Step 5: Rebalance Slots

```bash
# Redistribute slots evenly
redis-cli --cluster rebalance 127.0.0.1:7001 --cluster-use-empty-masters
```

**What happens:**
1. Cluster calculates fair slot distribution
2. Slots migrate from existing masters to new master
3. Migration happens live (no downtime)

### Step 6: Observe During Migration

```bash
# Watch slots moving
make watch-cluster
```

You'll see:
- Slots being migrated
- Brief ASK redirections during migration
- Final balanced state

### Before and After Comparison

```
Before (3 masters):
┌────────┐ ┌────────┐ ┌────────┐
│Master 1│ │Master 2│ │Master 3│
│ 5461   │ │ 5462   │ │ 5461   │
│ slots  │ │ slots  │ │ slots  │
└────────┘ └────────┘ └────────┘

After (4 masters):
┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐
│Master 1│ │Master 2│ │Master 3│ │Master 4│
│ ~4096  │ │ ~4096  │ │ ~4096  │ │ ~4096  │
│ slots  │ │ slots  │ │ slots  │ │ slots  │
└────────┘ └────────┘ └────────┘ └────────┘
```

### Step 7: Verify Application Still Works

```bash
# Create new event
./app create-event --name "After Scale" --seats 20

# Make reservation
./app reserve --event evt-XXX --seats A1,A2
```

Everything works transparently!

### Key Learning Points

1. **Zero-downtime scaling** via live slot migration
2. **Automatic rebalancing** distributes data evenly
3. **ASK redirections** handle in-flight migrations
4. **Application transparency** - clients adapt automatically

---

## Lab Summary Checklist

| Lab | Skill Learned | Verification Command |
|-----|--------------|---------------------|
| 1 | Cluster initialization | `make cluster-info` |
| 2 | Hash slot understanding | `CLUSTER KEYSLOT` |
| 3 | Atomic operations | `make demo` |
| 4 | Horizontal scaling | `make scale-up` |

### Common Troubleshooting

**Cluster state not OK:**
```bash
# Check which slots are missing
redis-cli -c -p 7001 CLUSTER SLOTS
```

**Connection refused:**
```bash
# Verify containers are running
docker compose ps
```

**CROSSSLOT error:**
```bash
# Ensure hash tags are used correctly
# {entity:id}:attribute format
```
