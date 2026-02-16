# Lab 3: Scale Up - Add a Master Node

## Overview

This lab demonstrates horizontal scaling by adding a new master node to Redis Cluster. You'll learn how slots are redistributed and how the cluster handles live migration without downtime.

---

## 1. Why Scale Up?

### When to Add More Masters

| Scenario | Indicator | Solution |
|----------|-----------|----------|
| Memory pressure | Each node near `maxmemory` | Add masters to distribute data |
| High throughput | CPU bottleneck on masters | More masters = more parallel processing |
| Hot keys | One node overloaded | Rebalance spreads load |
| Data growth | Approaching capacity | Scale horizontally |

### Scaling Options

```
Vertical Scaling (Scale Up)     Horizontal Scaling (Scale Out)
┌─────────────────────┐         ┌─────────┐ ┌─────────┐
│                     │         │ Master  │ │ Master  │
│   Bigger Server     │   VS    │    1    │ │    2    │
│   More RAM/CPU      │         └─────────┘ └─────────┘
│                     │         ┌─────────┐ ┌─────────┐
└─────────────────────┘         │ Master  │ │ Master  │
                                │    3    │ │    4    │
      Limited ceiling           └─────────┘ └─────────┘
                                   Unlimited growth
```

**Redis Cluster uses horizontal scaling** - add more nodes to increase capacity.

---

## 2. Before Scaling: Current State

### Check Current Cluster

```bash
make cluster-info
```

### Initial Topology (3 Masters)

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BEFORE: 3 MASTER NODES                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌───────────────┐   ┌───────────────┐   ┌───────────────┐          │
│  │   Master 1    │   │   Master 2    │   │   Master 3    │          │
│  │   Port 7001   │   │   Port 7002   │   │   Port 7003   │          │
│  │               │   │               │   │               │          │
│  │  Slots: 5461  │   │  Slots: 5462  │   │  Slots: 5461  │          │
│  │   (0-5460)    │   │ (5461-10922)  │   │(10923-16383)  │          │
│  └───────────────┘   └───────────────┘   └───────────────┘          │
│                                                                      │
│  Total: 16,384 slots distributed across 3 masters                   │
│  Each master: ~33% of data (~5,461 slots)                           │
└─────────────────────────────────────────────────────────────────────┘
```

### View Current Slot Distribution

```bash
make slot-info
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              REDIS CLUSTER SLOT DISTRIBUTION                      ║
╠══════════════════════════════════════════════════════════════════╣
║  Node             Slots      %      Bar                           ║
║  172.30.0.11:7001  5461   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.12:7002  5462   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.13:7003  5461   33.3%   ██████████░░░░░░░░░░           ║
╚══════════════════════════════════════════════════════════════════╝
```

---

## 3. The Scale-Up Process

### Command

```bash
make scale-up
```

### What Happens Behind the Scenes

```
┌─────────────────────────────────────────────────────────────────────┐
│                    SCALE-UP PROCESS                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Step 1: Start New Redis Container                                  │
│  ─────────────────────────────────                                  │
│  docker compose --profile scale up -d redis-7                       │
│                                                                     │
│  Step 2: Add Node to Cluster (Empty Master)                         │
│  ──────────────────────────────────────────                         │
│  redis-cli --cluster add-node 172.30.0.17:7007 172.30.0.11:7001     │
│                                                                     │
│  Step 3: Rebalance Slots                                            │
│  ────────────────────────                                           │
│  redis-cli --cluster rebalance --cluster-use-empty-masters          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 4. Step 1: Start New Container

### Docker Command

```bash
docker compose --profile scale up -d redis-7
```

### What Happens

1. Docker creates `redis-7` container
2. Container gets IP `172.30.0.17` on the cluster network
3. Redis starts on port `7007` with cluster mode enabled
4. Node is standalone (not yet part of cluster)

### Container Configuration

```yaml
redis-7:
  image: redis:7.2-alpine
  container_name: redis-7
  command: >
    redis-server /usr/local/etc/redis/redis.conf
    --port 7007
    --cluster-announce-ip 172.30.0.17
    --cluster-announce-port 7007
    --cluster-announce-bus-port 17007
  ports:
    - "7007:7007"
    - "17007:17007"
  networks:
    redis-cluster:
      ipv4_address: 172.30.0.17
```

---

## 5. Step 2: Add Node to Cluster

### Command

```bash
redis-cli --cluster add-node \
    172.30.0.17:7007 \    # New node to add
    172.30.0.11:7001      # Any existing cluster node
```

### What Happens

```
┌───────────────┐          ┌───────────────┐
│   redis-7     │  JOIN    │   Cluster     │
│   (new node)  │ ───────→ │  (existing)   │
│   Port 7007   │  REQUEST │  Port 7001    │
└───────────────┘          └───────────────┘
        │
        │ CLUSTER MEET exchanged
        │ Gossip protocol spreads node info
        ▼
┌─────────────────────────────────────────────┐
│  New node joins as EMPTY MASTER             │
│  - Known to all cluster nodes               │
│  - Has 0 slots assigned                     │
│  - Cannot serve any data yet                │
└─────────────────────────────────────────────┘
```

### Verify Node Joined

```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep 7007
```

**Output:**
```
<node-id> 172.30.0.17:7007@17007 master - 0 1705312345 0 connected
```

Note: **No slot range** at the end - the node has 0 slots!

---

## 6. Step 3: Rebalance Slots

### Command

```bash
redis-cli --cluster rebalance \
    172.30.0.11:7001 \
    --cluster-use-empty-masters
```

### The Rebalance Calculation

```
Before Rebalance (3 masters):
┌─────────┬─────────┬─────────┐
│ Master1 │ Master2 │ Master3 │
│  5461   │  5462   │  5461   │
└─────────┴─────────┴─────────┘

Target (4 masters):
16384 ÷ 4 = 4096 slots per master

After Rebalance:
┌─────────┬─────────┬─────────┬─────────┐
│ Master1 │ Master2 │ Master3 │ Master4 │
│  4096   │  4096   │  4096   │  4096   │
└─────────┴─────────┴─────────┴─────────┘
```

### Slots Moved Per Node

| Node | Before | After | Change |
|------|--------|-------|--------|
| Master 1 | 5,461 | 4,096 | -1,365 |
| Master 2 | 5,462 | 4,096 | -1,366 |
| Master 3 | 5,461 | 4,096 | -1,365 |
| Master 4 | 0 | 4,096 | +4,096 |

---

## 7. How Slot Migration Works

### Migration Process for Each Slot

```
┌─────────────────────────────────────────────────────────────────────┐
│                 SLOT MIGRATION PROCESS                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Source Node                              Target Node                │
│  (Master 1)                               (Master 4)                 │
│  ┌─────────────┐                         ┌─────────────┐            │
│  │ Slot 100    │                         │ Slot 100    │            │
│  │ MIGRATING   │ ─────────────────────→  │ IMPORTING   │            │
│  │             │                         │             │            │
│  │ Keys:       │     MIGRATE command     │ Keys:       │            │
│  │ - key1      │ ═════════════════════→  │ - key1      │            │
│  │ - key2      │                         │ - key2      │            │
│  │ - key3      │                         │ - key3      │            │
│  └─────────────┘                         └─────────────┘            │
│                                                                      │
│  1. CLUSTER SETSLOT 100 IMPORTING <source-id>  (on target)          │
│  2. CLUSTER SETSLOT 100 MIGRATING <target-id>  (on source)          │
│  3. CLUSTER GETKEYSINSLOT 100 100              (get keys in slot)   │
│  4. MIGRATE <target> 7007 "" 0 5000 KEYS key1 key2 key3             │
│  5. CLUSTER SETSLOT 100 NODE <target-id>       (on all nodes)       │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Key Points

- Keys are moved in batches (not one by one)
- Source keeps serving reads until key is migrated
- Atomic per-key: key exists in exactly one place at any time
- Process repeats for all ~4,096 slots being moved

---

## 8. Client Behavior During Migration

### MOVED vs ASK Redirects

```
┌─────────────────────────────────────────────────────────────────────┐
│              CLIENT REQUESTS DURING MIGRATION                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  MOVED Redirect (Permanent)                                         │
│  ──────────────────────────                                         │
│  • Slot ownership has changed                                       │
│  • Client SHOULD update slot→node cache                             │
│  • All future requests go to new node                               │
│                                                                      │
│  Client → Master1: GET {slot:100}:key                               │
│  Master1 → Client: MOVED 100 172.30.0.17:7007                       │
│  Client → Master4: GET {slot:100}:key                               │
│  Master4 → Client: "value"                                          │
│                                                                      │
│  ─────────────────────────────────────────────────────────────────  │
│                                                                      │
│  ASK Redirect (Temporary)                                           │
│  ────────────────────────                                           │
│  • Key might be migrating right now                                 │
│  • Client should NOT update cache                                   │
│  • Must send ASKING before the command                              │
│                                                                      │
│  Client → Master1: GET {slot:100}:key                               │
│  Master1 → Client: ASK 100 172.30.0.17:7007                         │
│  Client → Master4: ASKING                                           │
│  Client → Master4: GET {slot:100}:key                               │
│  Master4 → Client: "value"                                          │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Go-Redis Handles This Automatically

```go
// go-redis handles MOVED and ASK redirects transparently
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},
})

// This works during migration - redirects handled automatically
val, err := client.Get(ctx, "mykey").Result()
```

---

## 9. After Scaling: New State

### Check Updated Cluster

```bash
make cluster-info
```

### New Topology (4 Masters)

```
┌─────────────────────────────────────────────────────────────────────┐
│                    AFTER: 4 MASTER NODES                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐        │
│  │  Master 1  │ │  Master 2  │ │  Master 3  │ │  Master 4  │        │
│  │  Port 7001 │ │  Port 7002 │ │  Port 7003 │ │  Port 7007 │        │
│  │            │ │            │ │            │ │   (NEW)    │        │
│  │ Slots:4096 │ │ Slots:4096 │ │ Slots:4096 │ │ Slots:4096 │        │
│  │   (25%)    │ │   (25%)    │ │   (25%)    │ │   (25%)    │        │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘        │
│                                                                      │
│  Total: 16,384 slots distributed across 4 masters                   │
│  Each master: 25% of data (4,096 slots)                             │
└─────────────────────────────────────────────────────────────────────┘
```

### Verify Slot Distribution

```bash
make slot-info
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              REDIS CLUSTER SLOT DISTRIBUTION                      ║
╠══════════════════════════════════════════════════════════════════╣
║  Node             Slots      %      Bar                           ║
║  172.30.0.11:7001  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.12:7002  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.13:7003  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.17:7007  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
╚══════════════════════════════════════════════════════════════════╝
```

---

## 10. Watch Migration in Real-Time

### Monitor During Rebalance

```bash
make watch-cluster
```

### What You'll See

```
=== Redis Cluster Status (updated every 2s) ===

<id> 172.30.0.11:7001 master - 0 ... 1 connected 0-1364 5461-6826 ...
<id> 172.30.0.12:7002 master - 0 ... 2 connected 1365-2729 ...
<id> 172.30.0.13:7003 master - 0 ... 3 connected 10923-12287 ...
<id> 172.30.0.17:7007 master - 0 ... 4 connected 2730-4095 ...

--- Cluster Info ---
cluster_state:ok
cluster_slots_ok:16384
cluster_known_nodes:7
```

### Observe

- Slot ranges update as migration progresses
- `cluster_state:ok` throughout (no downtime)
- Slot ranges may be non-contiguous during migration

---

## 11. Verify Application Still Works

### Test Operations During/After Scale

```bash
# Create a new event
./app create-event --name "After Scale Test" --seats 20

# Make a reservation
./app reserve --event <event-id> --user user1 --seats A1,A2

# Check availability
./app availability --event <event-id>
```

### Why It Works

1. **Smart clients** handle MOVED/ASK redirects
2. **Hash tags** keep related data together (moves as unit)
3. **Cluster state** propagates via gossip protocol
4. **No single point of failure** - any node can route requests

---

## 12. Adding a Replica for the New Master

### The New Master Has No Replica!

```bash
make cluster-nodes | grep master
```

```
Master 1 (7001) ← Replica (7004)  ✓
Master 2 (7002) ← Replica (7005)  ✓
Master 3 (7003) ← Replica (7006)  ✓
Master 4 (7007) ← NO REPLICA      ✗  ← Risk!
```

### Add Replica for High Availability

```bash
make scale-add-replica
```

### Command Behind the Scenes

```bash
# Start redis-8 container
docker compose --profile scale up -d redis-8

# Add as replica of redis-7
redis-cli --cluster add-node \
    172.30.0.18:7008 \
    172.30.0.17:7007 \
    --cluster-slave
```

### After Adding Replica

```
Master 1 (7001) ← Replica (7004)  ✓
Master 2 (7002) ← Replica (7005)  ✓
Master 3 (7003) ← Replica (7006)  ✓
Master 4 (7007) ← Replica (7008)  ✓  ← Now protected!
```

---

## 13. Key Takeaways

### Zero-Downtime Scaling

| Aspect | Behavior |
|--------|----------|
| Read operations | Continue with MOVED/ASK redirects |
| Write operations | Continue with MOVED/ASK redirects |
| Cluster state | Always `ok` during migration |
| Data integrity | No data loss, atomic key migration |

### Capacity Changes

| Metric | 3 Masters | 4 Masters | Improvement |
|--------|-----------|-----------|-------------|
| Total memory | 300 MB | 400 MB | +33% |
| Throughput | 3x single | 4x single | +33% |
| Slots per node | 5,461 | 4,096 | -25% load |

### Best Practices

1. **Always add replica** after adding master
2. **Monitor during migration** with `watch-cluster`
3. **Test application** after scaling
4. **Plan for maintenance** windows for large rebalances

---

## 14. Troubleshooting

### New Node Shows 0 Slots

**Problem:** Node joined but has no slots

**Solution:** Run rebalance with `--cluster-use-empty-masters`
```bash
redis-cli --cluster rebalance localhost:7001 --cluster-use-empty-masters
```

### Rebalance Taking Too Long

**Problem:** Migration is slow

**Causes:**
- Large amount of data to move
- Network bandwidth limitation
- High cluster load

**Solutions:**
- Rebalance during low-traffic periods
- Use `--cluster-pipeline` for faster migration
- Move fewer slots at a time

### Client Errors During Migration

**Problem:** Application sees errors

**Check:**
- Is client library cluster-aware?
- Does it handle MOVED/ASK redirects?

**Solution:** Use official cluster client libraries (go-redis, jedis, etc.)

---

## 15. Summary & Commands

### Process Summary

```
1. Start container     → docker compose up -d redis-7
2. Add to cluster      → redis-cli --cluster add-node
3. Rebalance slots     → redis-cli --cluster rebalance
4. Add replica         → redis-cli --cluster add-node --cluster-slave
5. Verify              → make cluster-info
```

### Commands Reference

| Command | Purpose |
|---------|---------|
| `make scale-up` | Add new master + rebalance |
| `make scale-add-replica` | Add replica for new master |
| `make cluster-info` | View cluster state |
| `make slot-info` | View slot distribution |
| `make watch-cluster` | Monitor in real-time |

### Key Concepts

- **Horizontal scaling**: Add nodes to increase capacity
- **Slot rebalancing**: Redistribute 16,384 slots evenly
- **Live migration**: No downtime during scaling
- **MOVED/ASK**: Client redirect mechanisms
- **Replica pairing**: Every master needs a replica

---

## Next Lab

**Lab 4: Failover Testing** - Learn how Redis Cluster automatically handles master failures and promotes replicas.

```bash
# Preview Lab 4
make failover
make recover
```
