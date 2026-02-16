# Redis Cluster - 3 Hour Workshop
## NotebookLM Slide Format

---

## Slide 1: Workshop Overview

**Title:** Redis Cluster - 3 Hour Hands-On Workshop

**Duration:** 3 hours | **Level:** Intermediate

**Learning Outcomes:**
- Set up production-like 6-node Redis Cluster
- Master hash slots and key distribution patterns
- Scale cluster horizontally (add masters + replicas)
- Handle automatic failover scenarios
- Apply production best practices

**Prerequisites:**
- Basic Redis knowledge
- Docker installed
- Terminal/command line skills

**Module Breakdown:**
1. Cluster Setup (30 min)
2. Hash Slots & Distribution (35 min)
3. Scaling & Replication (40 min)
4. Automatic Failover (30 min)
5. Buffer/Q&A (25 min)

---

## Slide 2: Redis Cluster Architecture

**Title:** 6-Node Cluster Topology

```
         CLUSTER ARCHITECTURE (6 Nodes)

┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   MASTER 1   │  │   MASTER 2   │  │   MASTER 3   │
│ redis-1:7001 │  │ redis-2:7002 │  │ redis-3:7003 │
│              │  │              │  │              │
│ Slots 0-5460 │  │Slots 5461-   │  │Slots 10923-  │
│   (5,461)    │  │  10922       │  │  16383       │
│              │  │  (5,462)     │  │  (5,461)     │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       ▼ replication     ▼ replication     ▼ replication
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  REPLICA 4   │  │  REPLICA 5   │  │  REPLICA 6   │
│ redis-4:7004 │  │ redis-5:7005 │  │ redis-6:7006 │
└──────────────┘  └──────────────┘  └──────────────┘
```

**Key Concepts:**
- **16,384 hash slots** total (distributed across masters)
- Each master serves ~33% of data
- Replicas provide high availability (auto-failover)
- No single point of failure

---

## Slide 3: Module 1 - Cluster Setup

**Title:** Initialize Your First Cluster

**Commands:**
```bash
# Navigate to project
cd /path/to/redis-cluster-lab

# Start cluster (Docker Compose + init script)
make start

# Verify cluster health
make cluster-info
```

**Expected Output:**
```
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
cluster_size:3
```

**What Happened:**
- 6 Redis containers started
- Cluster topology initialized
- 16,384 slots distributed across 3 masters
- Replicas assigned to masters
- Automatic replication started

**Verification:** All checks show ✓ green status

---

## Slide 4: Hash Slots Explained

**Title:** How Redis Determines Key Location

**The Algorithm:**
```
Key → CRC16(key) → result mod 16384 → Slot (0-16383) → Node
```

**Example:**
```
Key: "user:1001"
CRC16("user:1001") = 50935
50935 % 16384 = 12539
Slot 12539 → Master 3 (owns slots 10923-16383)
```

**Slot Distribution:**
```
┌──────────────────────────────────────────────────┐
│         16,384 HASH SLOTS                        │
├────────────────┬────────────────┬────────────────┤
│   0-5460       │  5461-10922    │  10923-16383   │
│   Master 1     │  Master 2      │  Master 3      │
│   33.3%        │  33.3%         │  33.3%         │
└────────────────┴────────────────┴────────────────┘
```

**Key Insight:** Automatic load distribution across nodes

---

## Slide 5: Module 2 - Hash Tags

**Title:** Co-locating Related Keys

**The Problem - Without Hash Tags:**
```bash
event:123          → Slot 10456  → Master 2
event:123:seats    → Slot 3892   → Master 1
event:123:waitlist → Slot 15234  → Master 3

Result: Related data scattered across 3 nodes!
Cannot use: MULTI/EXEC, Lua scripts, MGET/MSET
```

**The Solution - With Hash Tags:**
```bash
{event:123}           → Slot 10456  → Master 2
{event:123}:seats     → Slot 10456  → Master 2
{event:123}:waitlist  → Slot 10456  → Master 2

Result: All data on SAME slot ✓
Can use: Atomic operations, Lua scripts, multi-key commands
```

**Syntax:** `{tag}:rest:of:key` - only `{tag}` is hashed

**Try It:**
```bash
make hash-tag-demo
make cross-slot-demo
```

---

## Slide 6: Hash Tag Patterns

**Title:** Common Production Patterns

**Recommended Patterns:**

| Use Case | Pattern | Example Keys |
|----------|---------|--------------|
| User data | `{user:ID}:field` | `{user:42}:profile`<br>`{user:42}:cart` |
| Orders | `{order:ID}:field` | `{order:99}:items`<br>`{order:99}:status` |
| Events | `{event:ID}:field` | `{event:abc}:seats`<br>`{event:abc}:waitlist` |

**Hands-On Exercise:**
```bash
# This FAILS - different slots
redis-cli MSET user:1 "Alice" user:2 "Bob"
# Error: CROSSSLOT

# This WORKS - same slot
redis-cli MSET "{user:1}:name" "Alice" "{user:1}:email" "alice@test.com"
redis-cli MGET "{user:1}:name" "{user:1}:email"
# Returns: ["Alice", "alice@test.com"]
```

**Best Practice:** Use entity IDs as hash tags

---

## Slide 7: Module 3 - Add Master Node

**Title:** Horizontal Scaling in Action

**Before Scaling (3 masters):**
```
┌─────────┬─────────┬─────────┐
│ Master1 │ Master2 │ Master3 │
│  5,461  │  5,462  │  5,461  │  slots
│  33.3%  │  33.3%  │  33.3%  │
└─────────┴─────────┴─────────┘
```

**Scale Up Command:**
```bash
make scale-up
```

**After Scaling (4 masters):**
```
┌─────────┬─────────┬─────────┬─────────┐
│ Master1 │ Master2 │ Master3 │ Master4 │
│  4,096  │  4,096  │  4,096  │  4,096  │  slots
│   25%   │   25%   │   25%   │   25%   │
└─────────┴─────────┴─────────┴─────────┘
```

**What Happened:**
1. redis-7 container started (port 7007)
2. Joined cluster as empty master
3. Slots rebalanced automatically (~1,365 from each master)
4. **Zero downtime** - cluster stays operational

---

## Slide 8: Slot Migration

**Title:** Zero-Downtime Migration Process

**Migration Timeline:**
```
Master 1:  [5461 slots] → [4096 slots] ─┐
Master 2:  [5462 slots] → [4096 slots] ─┼→ Master 4: [0] → [4096 slots]
Master 3:  [5461 slots] → [4096 slots] ─┘

Cluster State: ─────── OK throughout ───────
```

**Client Redirects:**

**MOVED (Permanent):**
```
Client → Old Node: GET key
Old Node → Client: MOVED 100 172.30.0.17:7007
Client updates cache, connects to new node
```

**ASK (Temporary - during migration):**
```
Client → Old Node: GET key
Old Node → Client: ASK 100 172.30.0.17:7007
Client → New Node: ASKING + GET key
Client does NOT update cache
```

**Smart Clients:** go-redis, jedis handle redirects automatically

---

## Slide 9: Add Replica Node

**Title:** High Availability Setup

**The Problem:**
```
Master 1 (7001) ← Replica (7004)  ✓ Protected
Master 2 (7002) ← Replica (7005)  ✓ Protected
Master 3 (7003) ← Replica (7006)  ✓ Protected
Master 4 (7007) ← NO REPLICA      ✗ VULNERABLE!

If Master 4 fails → 25% data LOST → Cluster FAILS
```

**The Solution:**
```bash
make scale-add-replica
```

**Result:**
```
Master 4 (7007) ← Replica 8 (7008)  ✓ Protected!
```

**Replication Process:**
1. redis-8 starts and joins cluster
2. Full sync: Master 4 sends RDB snapshot
3. Replica catches up with replication stream
4. Continuous async replication begins

**Verify:**
```bash
docker exec redis-8 redis-cli -p 7008 INFO replication
# master_link_status:up
# slave_repl_offset:123456
```

---

## Slide 10: Module 4 - Automatic Failover

**Title:** Simulating Master Failure

**Setup Monitoring:**
```bash
# Terminal 1 - Live cluster monitor
make watch-cluster

# Terminal 2 - Create test data
docker exec redis-1 redis-cli -c -p 7001 SET {test}:data "critical"
```

**Trigger Failover:**
```bash
# Stop Master 1
docker stop redis-1
```

**Failover Timeline:**

| Time | Event | Status |
|------|-------|--------|
| 0s | Master stops | Detection begins |
| 1-2s | Heartbeat missed | Nodes checking |
| 2-4s | **PFAIL** | Individual suspicion |
| 5s | **FAIL** | Majority confirms |
| 5-6s | Election | Replica requests votes |
| 6s | **Promotion** | Replica → Master |
| 6s+ | Operational | Cluster healthy ✓ |

**Total Downtime:** ~6-10 seconds (fully automatic)

---

## Slide 11: Failure Detection

**Title:** PFAIL vs FAIL States

**PFAIL (Potentially Failed):**
- Single node's opinion: "I can't reach this node"
- Spread via gossip protocol
- NOT enough to trigger failover

```
<id> 172.30.0.11:7001 master,pfail  ← Suspected
```

**FAIL (Confirmed Failed):**
- Majority of masters agree (2 of 3 in our cluster)
- Triggers failover election
- Replica with best replication offset wins

```
<id> 172.30.0.11:7001 master,fail   ← Confirmed
```

**Election Rules:**
```
DELAY = 500ms + random(0-500ms) + RANK × 1000ms

RANK 0 = Most up-to-date replica (wins fastest)
RANK 1 = Second best
etc.
```

**Winning replica needs majority votes:** 2 out of 3 masters

---

## Slide 12: Data Integrity After Failover

**Title:** Verify Zero Data Loss

**Test Data Access:**
```bash
# Connect to any node - auto-redirects
docker exec redis-2 redis-cli -c -p 7002 GET "{test}:data"
# Returns: "critical" ✓
```

**Why Data Is Safe:**
- Replica had synchronized copy
- Promoted replica already has all data
- Zero data loss (if replication caught up)

**Recover Failed Node:**
```bash
docker start redis-1
```

**Auto-Recovery Process:**
1. redis-1 reads cluster config from disk
2. Discovers topology changed (redis-4 is now master)
3. Automatically becomes replica of new master
4. Syncs data from new master

**New Topology:**
```
Master 4 (7004) ← Replica 1 (7001) [rejoined]
Master 2 (7002) ← Replica 5 (7005)
Master 3 (7003) ← Replica 6 (7006)
Master 7 (7007) ← Replica 8 (7008)
```

---

## Slide 13: Production Best Practices

**Title:** Deployment Checklist

**Key Naming Convention:**
```
Pattern: {entity:id}:attribute

Examples:
{user:123}:profile      ← Atomic operations
{user:123}:cart         ← on same slot
{user:123}:orders

{order:456}:items       ← Lua scripts
{order:456}:payment     ← work across keys
{order:456}:shipping
```

**Cluster Configuration:**
```conf
cluster-enabled yes
cluster-node-timeout 5000          # Failure detection
cluster-require-full-coverage yes  # Safety
appendonly yes                     # Persistence
maxmemory-policy allkeys-lru       # Eviction
```

**Pre-Production Checklist:**
```
✓ Enable persistence (AOF + RDB)
✓ Configure maxmemory limits
✓ Set up monitoring and alerts
✓ Test failover scenarios
✓ Plan backup strategy
✓ Configure passwords/TLS
✓ Use cluster-aware clients
✓ Document runbooks
```

---

## Slide 14: Common Pitfalls

**Title:** Mistakes to Avoid

**1. Cross-Slot Operations**
```bash
# ✗ BAD - Different slots
MGET user:1 user:2 user:3
# Error: CROSSSLOT

# ✓ GOOD - Same slot with hash tags
MGET {user}:1 {user}:2 {user}:3
```

**2. Missing Replicas**
```
✗ Master without replica → Single point of failure
✓ Every master has replica → Automatic failover
```

**3. Ignoring Redirects**
```go
// ✗ BAD - Direct connection
redis.Dial("tcp", "localhost:7001")

// ✓ GOOD - Cluster client
redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002"},
})
```

**4. Unsafe Node Removal**
```bash
# ✗ BAD - Stop master with slots
docker stop redis-7  # Data unavailable!

# ✓ GOOD - Reshard first
make scale-down  # Safely moves slots, then removes
```

---

## Slide 15: Workshop Summary

**Title:** Key Takeaways & Next Steps

**What You Mastered:**

| Concept | Description |
|---------|-------------|
| **Hash Slots** | 16,384 slots determine key location via CRC16 |
| **Hash Tags** | `{tag}` syntax co-locates related keys |
| **Replication** | Async replication provides HA |
| **Failover** | Automatic promotion in ~6-10 seconds |
| **Scaling** | Zero-downtime horizontal scaling |

**Essential Commands:**
```bash
make start                    # Start cluster
make cluster-info             # View status
make slot-info                # Slot distribution
make scale-up                 # Add master
make scale-add-replica        # Add replica
make failover                 # Test failover
make watch-cluster            # Monitor live
make key-slot KEY="mykey"     # Find key location
```

**Next Steps:**
- Practice failover scenarios
- Learn cluster backup/restore
- Explore Redis modules (JSON, Search)
- Study multi-datacenter patterns
- Benchmark performance

**Cleanup:**
```bash
make stop   # Stop containers
make clean  # Remove all data
```

---

## Quick Reference

**Troubleshooting:**

| Issue | Solution |
|-------|----------|
| CROSSSLOT error | Use hash tags `{tag}:key` |
| cluster_state:fail | Check all slots assigned |
| Slow failover | Tune `cluster-node-timeout` |
| High replication lag | Check network/disk |

**Client Examples:**

**Go:**
```go
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002"},
})
```

**Python:**
```python
from redis.cluster import RedisCluster
client = RedisCluster(startup_nodes=[{"host": "localhost", "port": "7001"}])
```

**Resources:**
- Redis Cluster Spec: redis.io/topics/cluster-spec
- Discord: discord.gg/redis
- Tool: RedisInsight (GUI management)
