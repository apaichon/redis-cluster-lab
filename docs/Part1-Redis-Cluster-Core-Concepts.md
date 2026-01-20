# Part 1: Redis Cluster Core Concepts

## Overview

This part covers the fundamental concepts of Redis Cluster, including what it is, how data is distributed, and how nodes communicate with each other.

---

## 1. What is Redis Cluster?

Redis Cluster is a distributed implementation of Redis that provides three key capabilities:

### Automatic Data Sharding
- Data is automatically distributed across multiple nodes
- No manual partitioning required
- Keys are mapped to specific nodes based on hash calculations

### High Availability
- Master-replica architecture ensures data redundancy
- Automatic failover when a master node fails
- Replicas are promoted to masters automatically

### Linear Scalability
- Add more nodes to increase capacity
- System scales horizontally as demand grows
- No single point of failure for data

---

## 2. Redis Cluster Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     REDIS CLUSTER                                │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │   Master 1  │  │   Master 2  │  │   Master 3  │              │
│  │ Slots 0-5460│  │Slots 5461-  │  │Slots 10923- │              │
│  │             │  │   10922     │  │   16383     │              │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘              │
│         │                │                │                      │
│  ┌──────▼──────┐  ┌──────▼──────┐  ┌──────▼──────┐              │
│  │  Replica 1  │  │  Replica 2  │  │  Replica 3  │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Key Points
- Minimum of 3 master nodes for a production cluster
- Each master should have at least one replica
- Data is partitioned across masters using hash slots
- Replicas provide read scaling and failover capability

---

## 3. Hash Slots Explained

Redis Cluster uses 16,384 hash slots to distribute data across nodes.

### The Hash Slot Formula

```
Key → CRC16(key) mod 16384 → Slot Number → Node
```

### How It Works

1. Take any key (e.g., "user:1001")
2. Calculate CRC16 checksum of the key
3. Take modulo 16384 to get a slot number (0-16383)
4. Route to the node responsible for that slot

### Practical Examples

```
"user:1001" → CRC16("user:1001") → 12539 → Master 3
"user:1002" → CRC16("user:1002") → 8174  → Master 2
"user:1003" → CRC16("user:1003") → 3421  → Master 1
```

### Slot Distribution

With 3 masters, slots are typically distributed as:
- Master 1: Slots 0-5460 (~5461 slots)
- Master 2: Slots 5461-10922 (~5462 slots)
- Master 3: Slots 10923-16383 (~5461 slots)

---

## 4. Hash Tags - Forcing Key Co-location

Hash tags allow you to control which slot a key belongs to, ensuring related keys stay on the same node.

### Hash Tag Syntax

Use curly braces `{...}` to specify the hash tag:

```
{event:123}           → slot calculated from "event:123"
{event:123}:seats     → slot calculated from "event:123" (same!)
{event:123}:waitlist  → slot calculated from "event:123" (same!)
```

### Why Hash Tags Are Critical

#### Problem Without Hash Tags
```
event:123          → Slot 7186 → Node 2
event:123:seats    → Slot 4521 → Node 1  ← Different node!
event:123:waitlist → Slot 9832 → Node 2  ← Different node!
```

#### Solution With Hash Tags
```
{event:123}           → Slot 7186 → Node 2
{event:123}:seats     → Slot 7186 → Node 2  ✓ Same node!
{event:123}:waitlist  → Slot 7186 → Node 2  ✓ Same node!
```

### When You Need Hash Tags

1. **Multi-key operations**: MGET, MSET require keys on same node
2. **Transactions**: MULTI/EXEC only work on single-node keys
3. **Lua scripts**: Scripts accessing multiple keys need co-location
4. **Atomic operations**: Any operation spanning multiple keys

---

## 5. Cluster Communication

Redis Cluster nodes communicate using two types of connections.

### Client Port vs Cluster Bus

```
┌────────────────┐         ┌────────────────┐
│    Client      │         │    Client      │
└───────┬────────┘         └───────┬────────┘
        │                          │
        ▼                          ▼
┌───────────────────────────────────────────┐
│              Client Ports (7001-7006)      │
├───────────────────────────────────────────┤
│                                           │
│  Node 1 ◄──────► Node 2 ◄──────► Node 3   │
│    │               │               │      │
│    ▼               ▼               ▼      │
│              Cluster Bus                   │
│           (Ports 17001-17006)              │
└───────────────────────────────────────────┘
```

### Client Ports (7001-7006)
- Used by applications to read/write data
- Standard Redis protocol
- One connection per node typically

### Cluster Bus (17001-17006)
- Internal node-to-node communication
- Binary protocol for efficiency
- Always port number + 10000

### Gossip Protocol

The cluster bus uses a gossip protocol for:
- **Node discovery**: New nodes learn about existing nodes
- **Failure detection**: Nodes share health information
- **Configuration updates**: Slot assignments are propagated
- **Master-replica coordination**: Replication state sharing

---

## 6. MOVED and ASK Redirections

When a client sends a command to the wrong node, Redis Cluster uses redirections.

### MOVED Redirection

Permanent redirection - the slot has moved to another node.

```
Client → Node 1: GET user:5000
Node 1 → Client: MOVED 8901 172.30.0.12:7002
Client → Node 2: GET user:5000
Node 2 → Client: "John Doe"
```

### When MOVED Occurs
- Slot permanently assigned to different node
- After cluster rebalancing
- Client should update its slot cache

### ASK Redirection

Temporary redirection - slot is being migrated.

```
Client → Node 1: GET user:6000
Node 1 → Client: ASK 9501 172.30.0.13:7003
Client → Node 3: ASKING
Client → Node 3: GET user:6000
Node 3 → Client: "Jane Doe"
```

### When ASK Occurs
- During live slot migration
- Slot temporarily split between nodes
- Client should NOT update cache

### Smart Clients

Modern Redis clients (like go-redis, jedis) are "smart clients" that:
- Cache the slot-to-node mapping
- Route commands directly to correct node
- Handle MOVED/ASK automatically
- Reduce latency by avoiding redirections

---

## 7. Key Takeaways

| Concept | Description | Why It Matters |
|---------|-------------|----------------|
| **Hash Slots** | 16,384 slots distribute data | Enables automatic sharding |
| **Hash Tags** | `{tag}` forces same slot | Required for multi-key operations |
| **Masters** | Hold data and serve writes | Primary data nodes |
| **Replicas** | Copy master data | Provide failover and read scaling |
| **Gossip Protocol** | Node-to-node communication | Enables distributed coordination |
| **MOVED/ASK** | Redirection mechanisms | Handle slot migrations gracefully |

---

## 8. Quick Reference

### Slot Calculation
```
slot = CRC16(key) % 16384
```

### Hash Tag Rules
- Only the part inside first `{...}` is hashed
- `{user}:profile` and `{user}:settings` → same slot
- `user:{123}:profile` → hashes "123"

### Cluster Requirements
- Minimum 3 master nodes
- Each master needs 1+ replicas for HA
- All nodes must be able to reach each other

### Common Ports
- Client: 7001, 7002, 7003, etc.
- Cluster bus: 17001, 17002, 17003, etc. (client port + 10000)
