# Lab 4: Scale Up - Add a Replica Node

## Overview

This lab demonstrates how to add a replica node to provide redundancy for a master. After adding a new master in Lab 3, it has no replica - making it a single point of failure. This lab fixes that vulnerability.

---

## 1. Why Replicas Matter

### The Risk Without Replicas

```
┌─────────────────────────────────────────────────────────────────────┐
│                    DANGER: MASTER WITHOUT REPLICA                   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Master 1 ←──── Replica 4    ✓ Protected                            │
│  Master 2 ←──── Replica 5    ✓ Protected                            │
│  Master 3 ←──── Replica 6    ✓ Protected                            │
│  Master 4 ←──── (NONE)       ✗ VULNERABLE!                          │
│                                                                     │
│  If Master 4 fails:                                                 │
│  • 4,096 slots become unavailable                                   │
│  • 25% of data is LOST                                              │
│  • cluster_state becomes FAIL                                       │
│  • Entire cluster stops accepting writes!                           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### What Replicas Provide

| Benefit | Description |
|---------|-------------|
| **High Availability** | Automatic failover when master dies |
| **Data Redundancy** | Copy of all master's data |
| **Read Scaling** | Can serve read requests (optional) |
| **Zero Data Loss** | Synchronized copy for recovery |

---

## 2. Replication Architecture

### Master-Replica Relationship

```
┌─────────────────────────────────────────────────────────────────────┐
│                    MASTER-REPLICA REPLICATION                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   ┌─────────────────┐                                               │
│   │     MASTER      │                                               │
│   │   (redis-7)     │                                               │
│   │   Port: 7007    │                                               │
│   │                 │                                               │
│   │  Slots: 4,096   │                                               │
│   │  Role: Primary  │                                               │
│   └────────┬────────┘                                               │
│            │                                                        │
│            │  Asynchronous Replication                              │
│            │  • All write commands sent to replica                  │
│            │  • Replica applies commands in order                   │
│            │  • Replication offset tracks progress                  │
│            │                                                        │
│            ▼                                                        │
│   ┌─────────────────┐                                               │
│   │    REPLICA      │                                               │
│   │   (redis-8)     │                                               │
│   │   Port: 7008    │                                               │
│   │                 │                                               │
│   │  Slots: (same)  │                                               │
│   │  Role: Standby  │                                               │
│   └─────────────────┘                                               │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Replication Flow

```
Client Write          Master (7007)              Replica (7008)
     │                     │                          │
     │  SET key "value"    │                          │
     │────────────────────>│                          │
     │                     │                          │
     │                     │  REPLICATE: SET key      │
     │                     │─────────────────────────>│
     │                     │                          │
     │  OK                 │                          │
     │<────────────────────│                          │
     │                     │                          │
```

---

## 3. Before Adding Replica

### Check Current State

```bash
make cluster-info
```

### Identify Unprotected Master

```bash
# Show masters and their replicas
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep -E "master|slave"
```

**Output:**
```
<id> 172.30.0.11:7001 master - ... 0-4095
<id> 172.30.0.12:7002 master - ... 4096-8191
<id> 172.30.0.13:7003 master - ... 8192-12287
<id> 172.30.0.17:7007 master - ... 12288-16383    ← No replica!
<id> 172.30.0.14:7004 slave <master-1-id> ...
<id> 172.30.0.15:7005 slave <master-2-id> ...
<id> 172.30.0.16:7006 slave <master-3-id> ...
```

### Visual State

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BEFORE: UNBALANCED REPLICAS                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐          │
│  │ Master 1 │   │ Master 2 │   │ Master 3 │   │ Master 4 │          │
│  │  (7001)  │   │  (7002)  │   │  (7003)  │   │  (7007)  │          │
│  └────┬─────┘   └────┬─────┘   └────┬─────┘   └────┬─────┘          │
│       │              │              │              │                │
│       ▼              ▼              ▼              ▼                │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐          │
│  │ Replica  │   │ Replica  │   │ Replica  │   │    ??    │          │
│  │  (7004)  │   │  (7005)  │   │  (7006)  │   │  (none)  │          │
│  │    ✓     │   │    ✓     │   │    ✓     │   │    ✗     │          │
│  └──────────┘   └──────────┘   └──────────┘   └──────────┘          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 4. The Add Replica Process

### Command

```bash
make scale-add-replica
```

### What Happens Behind the Scenes

```
┌─────────────────────────────────────────────────────────────────────┐
│                    ADD REPLICA PROCESS                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Step 1: Start Replica Container                                    │
│  ────────────────────────────────                                   │
│  docker compose --profile scale up -d redis-8                       │
│                                                                     │
│  Step 2: Get Master's Node ID                                       │
│  ─────────────────────────────                                      │
│  MASTER_ID=$(cluster nodes | grep 7007 | grep master | cut -f1)     │
│                                                                     │
│  Step 3: Add Node as Replica                                        │
│  ───────────────────────────                                        │
│  redis-cli --cluster add-node \                                     │
│      172.30.0.18:7008 \         # New replica                       │
│      172.30.0.11:7001 \         # Any cluster node                  │
│      --cluster-slave \          # Join as replica                   │
│      --cluster-master-id $MASTER_ID                                 │
│                                                                     │
│  Step 4: Initial Synchronization                                    │
│  ───────────────────────────────                                    │
│  • Replica connects to master                                       │
│  • Full sync (RDB snapshot) transferred                             │
│  • Replica catches up with replication stream                       │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 5. Step 1: Start Replica Container

### Docker Command

```bash
docker compose --profile scale up -d redis-8
```

### Container Configuration

```yaml
redis-8:
  image: redis:7.2-alpine
  container_name: redis-8
  command: >
    redis-server /usr/local/etc/redis/redis.conf
    --port 7008
    --cluster-announce-ip 172.30.0.18
    --cluster-announce-port 7008
    --cluster-announce-bus-port 17008
  ports:
    - "7008:7008"
    - "17008:17008"
  networks:
    redis-cluster:
      ipv4_address: 172.30.0.18
```

### Verify Container Running

```bash
docker exec redis-8 redis-cli -p 7008 ping
# Output: PONG
```

---

## 6. Step 2: Find Master's Node ID

### Command

```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":7007@" | grep master
```

### Output

```
a1b2c3d4e5f6... 172.30.0.17:7007@17007 master - 0 1705312345 7 connected 12288-16383
```

### Extract Node ID

```bash
MASTER_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7007@" | grep master | cut -d' ' -f1)
echo $MASTER_ID
# Output: a1b2c3d4e5f6...
```

**Why we need Master ID:**
- Explicitly tells Redis which master this replica should follow
- Prevents automatic assignment to wrong master
- Required for `--cluster-master-id` parameter

---

## 7. Step 3: Add Node as Replica

### Command

```bash
redis-cli --cluster add-node \
    172.30.0.18:7008 \           # New node address
    172.30.0.11:7001 \           # Existing cluster node
    --cluster-slave \            # Add as replica (not master)
    --cluster-master-id $MASTER_ID
```

### Parameters Explained

| Parameter | Value | Purpose |
|-----------|-------|---------|
| First address | `172.30.0.18:7008` | Node to add |
| Second address | `172.30.0.11:7001` | Entry point to cluster |
| `--cluster-slave` | (flag) | Join as replica, not master |
| `--cluster-master-id` | Node ID | Specific master to replicate |

### What Happens

1. New node joins cluster via gossip protocol
2. Node configured as replica of specified master
3. Replication connection established
4. Initial sync begins automatically

---

## 8. Step 4: Initial Synchronization

### Sync Process

```
┌─────────────────────────────────────────────────────────────────────┐
│                    INITIAL SYNC PROCESS                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Master (7007)                         Replica (7008)               │
│       │                                      │                       │
│       │  1. PSYNC ? -1 (request full sync)   │                       │
│       │<─────────────────────────────────────│                       │
│       │                                      │                       │
│       │  2. FULLRESYNC <id> <offset>         │                       │
│       │─────────────────────────────────────>│                       │
│       │                                      │                       │
│       │  3. RDB Snapshot (background save)   │                       │
│       │  ════════════════════════════════>   │                       │
│       │                                      │  Loading RDB...       │
│       │                                      │                       │
│       │  4. Buffered commands during sync    │                       │
│       │  ────────────────────────────────>   │                       │
│       │                                      │                       │
│       │  5. Continuous replication stream    │                       │
│       │  ────────────────────────────────>   │  Fully synced!       │
│       │                                      │                       │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Sync States

| State | Description |
|-------|-------------|
| `connect` | Connecting to master |
| `connecting` | Connection in progress |
| `sync` | Full synchronization in progress |
| `connected` | Fully synchronized, streaming updates |

---

## 9. Verify Replication Status

### Check Cluster Nodes

```bash
make cluster-info
```

### Check Replication on Replica

```bash
docker exec redis-8 redis-cli -p 7008 INFO replication
```

### Key Metrics

```
# Replication
role:slave                          ← Confirms replica role
master_host:172.30.0.17             ← Connected to correct master
master_port:7007
master_link_status:up               ← Replication is active
master_last_io_seconds_ago:0        ← Recently communicated
master_sync_in_progress:0           ← Not syncing (caught up)
slave_repl_offset:123456            ← Current replication position
slave_priority:100                  ← Failover priority
slave_read_only:1                   ← Cannot accept writes
```

### Verify Master-Replica Relationship

```bash
# From master's perspective
docker exec redis-7 redis-cli -p 7007 INFO replication
```

```
# Replication
role:master
connected_slaves:1                  ← Has one replica
slave0:ip=172.30.0.18,port=7008,state=online,offset=123456,lag=0
```

---

## 10. After Adding Replica

### New Cluster State

```
┌─────────────────────────────────────────────────────────────────────┐
│                    AFTER: ALL MASTERS PROTECTED                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐         │
│  │ Master 1 │   │ Master 2 │   │ Master 3 │   │ Master 4 │         │
│  │  (7001)  │   │  (7002)  │   │  (7003)  │   │  (7007)  │         │
│  └────┬─────┘   └────┬─────┘   └────┬─────┘   └────┬─────┘         │
│       │              │              │              │                 │
│       ▼              ▼              ▼              ▼                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐         │
│  │ Replica  │   │ Replica  │   │ Replica  │   │ Replica  │         │
│  │  (7004)  │   │  (7005)  │   │  (7006)  │   │  (7008)  │         │
│  │    ✓     │   │    ✓     │   │    ✓     │   │    ✓     │         │
│  └──────────┘   └──────────┘   └──────────┘   └──────────┘         │
│                                                                      │
│  All 4 masters now have replicas - cluster is fully redundant!      │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Verify Complete Protection

```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    awk '{print $2, $3}' | sort
```

**Expected Output:**
```
172.30.0.11:7001 master
172.30.0.12:7002 master
172.30.0.13:7003 master
172.30.0.14:7004 slave
172.30.0.15:7005 slave
172.30.0.16:7006 slave
172.30.0.17:7007 master
172.30.0.18:7008 slave      ← New replica!
```

---

## 11. Replication Lag Monitoring

### What is Replication Lag?

```
┌─────────────────────────────────────────────────────────────────────┐
│                    REPLICATION LAG                                   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Time ──────────────────────────────────────────────────────────>   │
│                                                                      │
│  Master:  [Write 1] [Write 2] [Write 3] [Write 4] [Write 5]         │
│                 │         │         │         │         │           │
│                 ▼         ▼         ▼         ▼         │           │
│  Replica: [Write 1] [Write 2] [Write 3] [Write 4]       │           │
│                                                    ─────┘           │
│                                                      LAG            │
│                                                                      │
│  Lag = Master offset - Replica offset                               │
│  Lag = 0 means replica is fully caught up                           │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Check Lag

```bash
# On master - shows lag for each replica
docker exec redis-7 redis-cli -p 7007 INFO replication | grep lag
```

**Output:**
```
slave0:ip=172.30.0.18,port=7008,state=online,offset=123456,lag=0
```

### Acceptable Lag Values

| Lag | Status | Action |
|-----|--------|--------|
| 0 | Perfect | Normal operation |
| 1-5 | Good | Normal under load |
| 5-30 | Warning | Monitor closely |
| 30+ | Critical | Check network/disk |

---

## 12. Reading from Replicas (Optional)

### Enable Replica Reads

By default, replicas reject read commands. To enable:

```bash
# On replica
docker exec redis-8 redis-cli -p 7008 READONLY
```

### Go-Redis Configuration

```go
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},

    // Route reads to replicas
    RouteByLatency: true,   // Route to lowest latency node
    // OR
    RouteRandomly: true,    // Random node (master or replica)
    // OR
    ReadOnly: true,         // Prefer replicas for reads
})
```

### Caution

```
┌─────────────────────────────────────────────────────────────────────┐
│  ⚠️  WARNING: Replica reads may return stale data!                   │
│                                                                      │
│  • Replication is asynchronous                                      │
│  • Replica may be milliseconds behind master                        │
│  • Use only when eventual consistency is acceptable                 │
│                                                                      │
│  Good for: Analytics, dashboards, read-heavy workloads              │
│  Bad for: Real-time inventory, financial transactions               │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 13. Multiple Replicas Per Master

### Why Multiple Replicas?

| Benefit | Description |
|---------|-------------|
| **Higher availability** | Survive multiple failures |
| **Read scaling** | More nodes for read traffic |
| **Geographic distribution** | Replicas in different regions |

### Adding Second Replica

```bash
# Start another container (redis-9)
# Add to same master
redis-cli --cluster add-node \
    172.30.0.19:7009 \
    172.30.0.11:7001 \
    --cluster-slave \
    --cluster-master-id $MASTER_ID
```

### Topology with Multiple Replicas

```
            ┌─────────────┐
            │   Master    │
            │   (7007)    │
            └──────┬──────┘
                   │
        ┌──────────┼──────────┐
        │          │          │
        ▼          ▼          ▼
  ┌──────────┐ ┌──────────┐ ┌──────────┐
  │ Replica  │ │ Replica  │ │ Replica  │
  │  (7008)  │ │  (7009)  │ │  (7010)  │
  └──────────┘ └──────────┘ └──────────┘
```

---

## 14. Troubleshooting

### Replica Not Connecting

**Problem:** `master_link_status:down`

**Check:**
```bash
docker exec redis-8 redis-cli -p 7008 INFO replication
```

**Solutions:**
1. Verify network connectivity between nodes
2. Check if master is running
3. Verify cluster-announce-ip is correct

### Sync Taking Too Long

**Problem:** `master_sync_in_progress:1` for extended time

**Causes:**
- Large dataset
- Slow network
- Disk I/O bottleneck

**Solutions:**
```bash
# Check sync progress
docker exec redis-8 redis-cli -p 7008 INFO replication | grep sync
```

### Replica Shows Wrong Master

**Problem:** Replica connected to different master than intended

**Solution:**
```bash
# Force replica to follow correct master
docker exec redis-8 redis-cli -p 7008 CLUSTER REPLICATE $CORRECT_MASTER_ID
```

### High Replication Lag

**Problem:** `lag` value consistently high

**Causes:**
- Network latency
- Master under heavy write load
- Replica disk too slow

**Solutions:**
- Check network between master and replica
- Consider faster storage for replica
- Reduce write load during peak times

---

## 15. Summary & Commands

### Process Summary

```
1. Start container      → docker compose up -d redis-8
2. Find master ID       → cluster nodes | grep 7007
3. Add as replica       → --cluster add-node --cluster-slave
4. Verify replication   → INFO replication
5. Monitor lag          → Check slave offset and lag
```

### Commands Reference

| Command | Purpose |
|---------|---------|
| `make scale-add-replica` | Add replica to new master |
| `INFO replication` | Check replication status |
| `CLUSTER NODES` | View all nodes and roles |
| `CLUSTER REPLICATE <id>` | Change replica's master |
| `READONLY` | Enable reads on replica |

### Key Concepts

| Concept | Description |
|---------|-------------|
| **Replica** | Read-only copy of master's data |
| **Async replication** | Writes acknowledged before replicating |
| **Full sync** | Initial RDB transfer to new replica |
| **Partial sync** | Resume from replication offset |
| **Replication lag** | Delay between master and replica |

### Best Practices

1. **Every master needs at least one replica**
2. **Monitor replication lag** in production
3. **Place replicas on different servers/racks** than masters
4. **Test failover** regularly to ensure replicas can take over
5. **Consider multiple replicas** for critical data

---

## Next Lab

**Lab 5: Failover Testing** - Test automatic failover when a master fails and watch the replica get promoted.

```bash
# Preview Lab 5
make failover
make watch-cluster
make recover
```
