# Lab 6: Scale Down - Remove a Node

## Overview

This lab demonstrates how to safely remove a node from Redis Cluster. Whether you're decommissioning hardware, reducing costs, or consolidating resources, removing nodes requires careful planning to ensure zero data loss and no downtime.

---

## 1. Why Scale Down?

### When to Remove Nodes

| Scenario | Indicator | Action |
|----------|-----------|--------|
| **Cost optimization** | Over-provisioned resources | Remove excess capacity |
| **Low utilization** | Memory/CPU usage < 30% | Consolidate workload |
| **Hardware decommission** | Server end-of-life | Migrate data off node |
| **Cluster simplification** | Too many nodes to manage | Reduce complexity |
| **Disaster recovery test** | Planned maintenance | Test graceful removal |

### The Challenge

```
┌─────────────────────────────────────────────────────────────────────┐
│                 THE SCALE-DOWN CHALLENGE                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  You CAN'T just turn off a master node!                             │
│                                                                     │
│  If you stop a master directly:                                     │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  ✗ Data on that node becomes INACCESSIBLE                   │    │
│  │  ✗ Slots owned by node become UNAVAILABLE                   │    │
│  │  ✗ Cluster state becomes FAIL                               │    │
│  │  ✗ ALL writes to cluster REJECTED                           │    │
│  └─────────────────────────────────────────────────────────────┘    │
│                                                                     │
│  Correct approach:                                                  │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  1. Move all slots AWAY from the node first                 │    │
│  │  2. Remove empty node from cluster                          │    │
│  │  3. Then stop the container                                 │    │
│  └─────────────────────────────────────────────────────────────┘    │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Before Scaling: Current State

### Check Current Cluster

```bash
make cluster-info
```

### Current Topology (4 Masters + 4 Replicas)

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BEFORE: 4 MASTER NODES                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐        │
│  │  Master 1  │ │  Master 2  │ │  Master 3  │ │  Master 4  │        │
│  │  Port 7001 │ │  Port 7002 │ │  Port 7003 │ │  Port 7007 │        │
│  │            │ │            │ │            │ │  (TO REMOVE)│       │
│  │ Slots:4096 │ │ Slots:4096 │ │ Slots:4096 │ │ Slots:4096 │        │
│  │   (25%)    │ │   (25%)    │ │   (25%)    │ │   (25%)    │        │
│  └─────┬──────┘ └─────┬──────┘ └─────┬──────┘ └─────┬──────┘        │
│        │              │              │              │               │
│        ▼              ▼              ▼              ▼               │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐        │
│  │ Replica 4  │ │ Replica 5  │ │ Replica 6  │ │ Replica 8  │        │
│  │  Port 7004 │ │  Port 7005 │ │  Port 7006 │ │  Port 7008 │        │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘        │
│                                                                     │
│  Total: 16,384 slots across 4 masters (8 nodes total)               │
└─────────────────────────────────────────────────────────────────────┘
```

### View Current Slot Distribution

```bash
make slot-info
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              REDIS CLUSTER SLOT DISTRIBUTION                     ║
╠══════════════════════════════════════════════════════════════════╣
║  Node             Slots      %      Bar                          ║
║  172.30.0.11:7001  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.12:7002  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.13:7003  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.17:7007  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
╚══════════════════════════════════════════════════════════════════╝
```

### Identify Node to Remove

```bash
# Get node ID for the node we want to remove (redis-7, port 7007)
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":7007@"
```

**Output:**
```
a1b2c3d4e5f6... 172.30.0.17:7007@17007 master - 0 1705312345 7 connected 12288-16383
```

---

## 3. The Scale-Down Process

### Command

```bash
make scale-down
```

### What Happens Behind the Scenes

```
┌─────────────────────────────────────────────────────────────────────┐
│                    SCALE-DOWN PROCESS                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Phase 1: Remove Replica First (if exists)                          │
│  ─────────────────────────────────────────                          │
│  redis-cli --cluster del-node <cluster> <replica-id>                │
│  docker stop redis-8                                                │
│                                                                     │
│  Phase 2: Reshard Slots Away from Master                            │
│  ────────────────────────────────────────                           │
│  redis-cli --cluster reshard <cluster> \                            │
│      --cluster-from <master-7-id> \                                 │
│      --cluster-to <master-1-id> \                                   │
│      --cluster-slots 1366 \                                         │
│      --cluster-yes                                                  │
│  (Repeat for master-2 and master-3)                                 │
│                                                                     │
│  Phase 3: Remove Empty Master                                       │
│  ────────────────────────────                                       │
│  redis-cli --cluster del-node <cluster> <master-7-id>               │
│                                                                     │
│  Phase 4: Stop Container                                            │
│  ───────────────────────                                            │
│  docker stop redis-7                                                │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 4. Phase 1: Remove Replica First

### Why Remove Replica First?

```
┌─────────────────────────────────────────────────────────────────────┐
│                 WHY REPLICA FIRST?                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  If you remove master first:                                        │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  Master 7 removed → Replica 8 has no master                   │  │
│  │                   → Replica 8 might try to become master!     │  │
│  │                   → Causes cluster confusion                  │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  Correct order:                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  1. Remove Replica 8 first (it has no data responsibility)    │  │
│  │  2. Then handle Master 7 (reshard + remove)                   │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Find Replica Node ID

```bash
# Find replica of master 7007
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep "slave" | grep "7008"
```

**Output:**
```
b2c3d4e5f6a1... 172.30.0.18:7008@17008 slave a1b2c3d4e5f6... 0 1705312346 7 connected
```

### Remove Replica from Cluster

```bash
# Get the replica's node ID
REPLICA_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7008@" | cut -d' ' -f1)

# Remove from cluster
docker exec redis-1 redis-cli -p 7001 --cluster del-node \
    172.30.0.11:7001 \
    $REPLICA_ID
```

**Output:**
```
>>> Removing node b2c3d4e5f6a1... from cluster 172.30.0.11:7001
>>> Sending CLUSTER FORGET messages to the cluster...
>>> SHUTDOWN the node.
```

### Stop Replica Container

```bash
docker stop redis-8
docker rm redis-8
```

### Verify Replica Removed

```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep -c "7008"
# Output: 0 (not found)
```

---

## 5. Phase 2: Reshard Slots Away from Master

### The Math

```
┌─────────────────────────────────────────────────────────────────────┐
│                    SLOT REDISTRIBUTION MATH                         │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Current State (4 masters):                                         │
│  ┌─────────┬─────────┬─────────┬─────────┐                          │
│  │ Master1 │ Master2 │ Master3 │ Master4 │                          │
│  │  4,096  │  4,096  │  4,096  │  4,096  │                          │
│  └─────────┴─────────┴─────────┴─────────┘                          │
│                                                                     │
│  Target State (3 masters):                                          │
│  16,384 ÷ 3 = 5,461.33 → round to 5,461 / 5,462 / 5,461             │
│                                                                     │
│  ┌─────────┬─────────┬─────────┐                                    │
│  │ Master1 │ Master2 │ Master3 │                                    │
│  │  5,461  │  5,462  │  5,461  │                                    │
│  └─────────┴─────────┴─────────┘                                    │
│                                                                     │
│  Slots to move FROM Master4:                                        │
│  • To Master1: 5,461 - 4,096 = +1,365 slots                         │
│  • To Master2: 5,462 - 4,096 = +1,366 slots                         │
│  • To Master3: 5,461 - 4,096 = +1,365 slots                         │
│  • Total moved: 4,096 (all of Master4's slots)                      │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Get Node IDs

```bash
# Get all node IDs
MASTER_1_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7001@" | grep master | cut -d' ' -f1)
MASTER_2_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7002@" | grep master | cut -d' ' -f1)
MASTER_3_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7003@" | grep master | cut -d' ' -f1)
MASTER_4_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7007@" | grep master | cut -d' ' -f1)

echo "Master 1: $MASTER_1_ID"
echo "Master 2: $MASTER_2_ID"
echo "Master 3: $MASTER_3_ID"
echo "Master 4 (to remove): $MASTER_4_ID"
```

### Reshard to Master 1

```bash
docker exec redis-1 redis-cli -p 7001 --cluster reshard \
    172.30.0.11:7001 \
    --cluster-from $MASTER_4_ID \
    --cluster-to $MASTER_1_ID \
    --cluster-slots 1365 \
    --cluster-yes
```

**Output:**
```
>>> Performing Cluster Check (using node 172.30.0.11:7001)
...
>>> Resharding plan:
    Moving slot 12288 from a1b2c3d4e5f6...
    Moving slot 12289 from a1b2c3d4e5f6...
    ...
    Moving slot 13652 from a1b2c3d4e5f6...
>>> Moving slot 12288 from 172.30.0.17:7007 to 172.30.0.11:7001
>>> Moving slot 12289 from 172.30.0.17:7007 to 172.30.0.11:7001
...
```

### Reshard to Master 2

```bash
docker exec redis-1 redis-cli -p 7001 --cluster reshard \
    172.30.0.11:7001 \
    --cluster-from $MASTER_4_ID \
    --cluster-to $MASTER_2_ID \
    --cluster-slots 1366 \
    --cluster-yes
```

### Reshard to Master 3

```bash
docker exec redis-1 redis-cli -p 7001 --cluster reshard \
    172.30.0.11:7001 \
    --cluster-from $MASTER_4_ID \
    --cluster-to $MASTER_3_ID \
    --cluster-slots 1365 \
    --cluster-yes
```

### Verify Master 4 Has Zero Slots

```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":7007@"
```

**Output:**
```
a1b2c3d4e5f6... 172.30.0.17:7007@17007 master - 0 1705312400 7 connected
```

**Note:** No slot range at the end - the node now has 0 slots!

---

## 6. How Slot Migration Works (Outbound)

### Migration Process

```
┌─────────────────────────────────────────────────────────────────────┐
│                 SLOT MIGRATION (SCALE DOWN)                         │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Source: Master 4 (being removed)     Target: Master 1              │
│  ┌─────────────────────┐              ┌─────────────────────┐       │
│  │   Port 7007         │              │   Port 7001         │       │
│  │                     │              │                     │       │
│  │  Slot 12288:        │              │  Slot 12288:        │       │
│  │  ┌─────────────┐    │    MIGRATE   │  ┌─────────────┐    │       │
│  │  │ key1: val1  │────┼─────────────►│  │ key1: val1  │    │       │
│  │  │ key2: val2  │────┼─────────────►│  │ key2: val2  │    │       │
│  │  │ key3: val3  │────┼─────────────►│  │ key3: val3  │    │       │
│  │  └─────────────┘    │              │  └─────────────┘    │       │
│  │                     │              │                     │       │
│  │  After: EMPTY       │              │  After: +3 keys     │       │
│  └─────────────────────┘              └─────────────────────┘       │
│                                                                     │
│  Process for each slot:                                             │
│  1. Mark slot as MIGRATING on source                                │
│  2. Mark slot as IMPORTING on target                                │
│  3. Move all keys in slot via MIGRATE command                       │
│  4. Update slot ownership on ALL nodes                              │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Timeline Visualization

```
┌─────────────────────────────────────────────────────────────────────┐
│                    MIGRATION TIMELINE                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Time ─────────────────────────────────────────────────────────►    │
│                                                                     │
│  Master 4:  [4096 slots] → [2731 slots] → [1365 slots] → [0 slots]  │
│                    │             │              │             │     │
│                    ▼             ▼              ▼             ▼     │
│  Master 1:  [4096 slots] → [5461 slots]                             │
│  Master 2:  [4096 slots] ────────────────→ [5462 slots]             │
│  Master 3:  [4096 slots] ─────────────────────────────→ [5461 slots]│
│                                                                     │
│  Cluster State: ──────────── OK throughout ──────────────────────   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 7. Phase 3: Remove Empty Master

### Verify Node Has No Slots

```bash
# Count slots on Master 4
docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7007@" | grep -o "[0-9]*-[0-9]*" | wc -l
# Should output: 0
```

### Remove Node from Cluster

```bash
docker exec redis-1 redis-cli -p 7001 --cluster del-node \
    172.30.0.11:7001 \
    $MASTER_4_ID
```

**Output:**
```
>>> Removing node a1b2c3d4e5f6... from cluster 172.30.0.11:7001
>>> Sending CLUSTER FORGET messages to the cluster...
>>> SHUTDOWN the node.
```

### What Happens

```
┌─────────────────────────────────────────────────────────────────────┐
│                    DEL-NODE PROCESS                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. Cluster checks node has 0 slots (required!)                     │
│                                                                     │
│  2. CLUSTER FORGET sent to all other nodes                          │
│     ┌─────────┐                                                     │
│     │ Master1 │ ← CLUSTER FORGET <master-4-id>                      │
│     │ Master2 │ ← CLUSTER FORGET <master-4-id>                      │
│     │ Master3 │ ← CLUSTER FORGET <master-4-id>                      │
│     │ Replica4│ ← CLUSTER FORGET <master-4-id>                      │
│     │ Replica5│ ← CLUSTER FORGET <master-4-id>                      │
│     │ Replica6│ ← CLUSTER FORGET <master-4-id>                      │
│     └─────────┘                                                     │
│                                                                     │
│  3. Node removed from cluster topology                              │
│                                                                     │
│  4. SHUTDOWN sent to the node being removed                         │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 8. Phase 4: Stop and Clean Up Container

### Stop Container

```bash
docker stop redis-7
```

### Remove Container (Optional)

```bash
docker rm redis-7
```

### Clean Up Data (Optional)

```bash
# If you want to remove the data volume
docker volume rm redis-cluster-labs_redis-7-data
```

### Verify Node Gone

```bash
# Should show only 6 nodes now
docker exec redis-1 redis-cli -p 7001 cluster nodes | wc -l
# Output: 6

# Verify no 7007 or 7008
docker exec redis-1 redis-cli -p 7001 cluster nodes
```

---

## 9. After Scaling: New State

### Check Updated Cluster

```bash
make cluster-info
```

### New Topology (3 Masters + 3 Replicas)

```
┌─────────────────────────────────────────────────────────────────────┐
│                    AFTER: 3 MASTER NODES                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌───────────────┐   ┌───────────────┐   ┌───────────────┐          │
│  │   Master 1    │   │   Master 2    │   │   Master 3    │          │
│  │   Port 7001   │   │   Port 7002   │   │   Port 7003   │          │
│  │               │   │               │   │               │          │
│  │  Slots: 5461  │   │  Slots: 5462  │   │  Slots: 5461  │          │
│  │   (33.3%)     │   │   (33.3%)     │   │   (33.3%)     │          │
│  └───────┬───────┘   └───────┬───────┘   └───────┬───────┘          │
│          │                   │                   │                  │
│          ▼                   ▼                   ▼                  │
│  ┌───────────────┐   ┌───────────────┐   ┌───────────────┐          │
│  │   Replica 4   │   │   Replica 5   │   │   Replica 6   │          │
│  │   Port 7004   │   │   Port 7005   │   │   Port 7006   │          │
│  └───────────────┘   └───────────────┘   └───────────────┘          │
│                                                                     │
│  Total: 16,384 slots across 3 masters (6 nodes total)               │
│  ✓ Master 4 (7007) removed                                          │
│  ✓ Replica 8 (7008) removed                                         │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Verify Slot Distribution

```bash
make slot-info
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              REDIS CLUSTER SLOT DISTRIBUTION                     ║
╠══════════════════════════════════════════════════════════════════╣
║  Node             Slots      %      Bar                          ║
║  172.30.0.11:7001  5461   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.12:7002  5462   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.13:7003  5461   33.3%   ██████████░░░░░░░░░░           ║
╚══════════════════════════════════════════════════════════════════╝
```

### Verify Cluster Health

```bash
docker exec redis-1 redis-cli -p 7001 cluster info | grep -E "state|slots|nodes"
```

**Output:**
```
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_slots_pfail:0
cluster_slots_fail:0
cluster_known_nodes:6
```

---

## 10. Client Behavior During Scale Down

### Redirects During Migration

```
┌─────────────────────────────────────────────────────────────────────┐
│              CLIENT REQUESTS DURING SCALE DOWN                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Before Migration Complete:                                         │
│  ───────────────────────────                                        │
│                                                                     │
│  Client → Master4: GET key (slot 12500)                             │
│  Master4 → Client: ASK 12500 172.30.0.11:7001                       │
│  Client → Master1: ASKING                                           │
│  Client → Master1: GET key                                          │
│  Master1 → Client: "value"                                          │
│                                                                     │
│  After Migration Complete:                                          │
│  ──────────────────────────                                         │
│                                                                     │
│  Client → Master4: GET key (slot 12500)                             │
│  Master4 → Client: MOVED 12500 172.30.0.11:7001                     │
│  Client updates slot cache                                          │
│  Client → Master1: GET key                                          │
│  Master1 → Client: "value"                                          │
│                                                                     │
│  After Node Removed:                                                │
│  ────────────────────                                               │
│                                                                     │
│  Client → Master1: GET key (slot 12500)  ← Direct, from cache       │
│  Master1 → Client: "value"                                          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Go-Redis Handles This Automatically

```go
// Client continues working during scale-down
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{
        "localhost:7001",
        "localhost:7002",
        "localhost:7003",
        // Note: 7007 can be listed but will be removed from routing
    },
})

// This works throughout the migration
val, err := client.Get(ctx, "mykey").Result()
// Redirects handled automatically!
```

---

## 11. Watch Migration in Real-Time

### Monitor During Scale Down

```bash
make watch-cluster
```

### What You'll See

```
=== Redis Cluster Status (updated every 2s) ===

# During migration - slots moving from 7007
<id> 172.30.0.11:7001 master - 0 ... connected 0-4095 12288-13652
<id> 172.30.0.12:7002 master - 0 ... connected 4096-8191 13653-15018
<id> 172.30.0.13:7003 master - 0 ... connected 8192-12287 15019-16383
<id> 172.30.0.17:7007 master - 0 ... connected    ← slots disappearing!

# After migration complete - 7007 has no slots
<id> 172.30.0.11:7001 master - 0 ... connected 0-5460
<id> 172.30.0.12:7002 master - 0 ... connected 5461-10922
<id> 172.30.0.13:7003 master - 0 ... connected 10923-16383
<id> 172.30.0.17:7007 master - 0 ... connected    ← EMPTY!

# After del-node - 7007 gone
<id> 172.30.0.11:7001 master - 0 ... connected 0-5460
<id> 172.30.0.12:7002 master - 0 ... connected 5461-10922
<id> 172.30.0.13:7003 master - 0 ... connected 10923-16383

--- Cluster Info ---
cluster_state:ok              ← Always OK during process!
cluster_slots_ok:16384
cluster_known_nodes:6         ← Reduced from 8
```

---

## 12. Alternative: Using Rebalance

### Automatic Slot Distribution

Instead of manually calculating slots, you can use rebalance:

```bash
# First, reshard ALL slots away from the node to remove
docker exec redis-1 redis-cli -p 7001 --cluster reshard \
    172.30.0.11:7001 \
    --cluster-from $MASTER_4_ID \
    --cluster-to $MASTER_1_ID,$MASTER_2_ID,$MASTER_3_ID \
    --cluster-slots 4096 \
    --cluster-yes
```

### Or Use Rebalance After Manual Removal

```bash
# After removing node, if slots are uneven
redis-cli --cluster rebalance 172.30.0.11:7001
```

---

## 13. Removing a Replica-Only Node

### Simpler Process

If you're removing a replica (not a master), the process is simpler:

```bash
# 1. Get replica node ID
REPLICA_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | \
    grep ":7004@" | grep slave | cut -d' ' -f1)

# 2. Remove from cluster (no reshard needed!)
docker exec redis-1 redis-cli -p 7001 --cluster del-node \
    172.30.0.11:7001 \
    $REPLICA_ID

# 3. Stop container
docker stop redis-4
```

### Why No Reshard?

```
┌─────────────────────────────────────────────────────────────────────┐
│  Replicas have NO SLOTS - they only hold copies of master's data    │
│                                                                     │
│  Master:  Owns slots, serves reads/writes                           │
│  Replica: No slots, only replicates master's data                   │
│                                                                     │
│  So removing replica = just forget + stop (no data migration!)      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 14. Troubleshooting

### Cannot Remove Node: Has Slots

**Problem:**
```
[ERR] Node 172.30.0.17:7007 is not empty! Reshard data away and try again.
```

**Solution:**
```bash
# Check how many slots the node has
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":7007@"

# Reshard remaining slots away
redis-cli --cluster reshard 172.30.0.11:7001 \
    --cluster-from $MASTER_4_ID \
    --cluster-to $MASTER_1_ID \
    --cluster-slots <remaining_slots> \
    --cluster-yes
```

### Node Stuck in Cluster After del-node

**Problem:** Other nodes still see the removed node

**Solution:**
```bash
# Manually forget on each node
docker exec redis-1 redis-cli -p 7001 CLUSTER FORGET $OLD_NODE_ID
docker exec redis-2 redis-cli -p 7002 CLUSTER FORGET $OLD_NODE_ID
docker exec redis-3 redis-cli -p 7003 CLUSTER FORGET $OLD_NODE_ID
```

### Migration Stuck

**Problem:** Slots stuck in MIGRATING/IMPORTING state

**Check:**
```bash
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep -E "migrating|importing"
```

**Fix:**
```bash
# Force slot ownership update
redis-cli --cluster fix 172.30.0.11:7001
```

### Application Errors During Scale Down

**Problem:** Clients getting connection errors

**Causes:**
- Client still trying to connect to removed node
- Client cache not updated

**Solutions:**
```go
// Ensure client can discover new topology
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},

    // Important settings for resilience
    MaxRedirects:   8,        // Follow redirects
    ReadOnly:       false,
    RouteByLatency: true,

    // Refresh cluster info periodically
    // (go-redis does this automatically)
})
```

---

## 15. Best Practices

### Pre-Scale-Down Checklist

```
┌─────────────────────────────────────────────────────────────────────┐
│                PRE-SCALE-DOWN CHECKLIST                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  □ Verify cluster is healthy (cluster_state:ok)                     │
│  □ Check remaining nodes have enough memory for additional data     │
│  □ Plan for maintenance window (if large dataset)                   │
│  □ Notify stakeholders of potential brief latency increase          │
│  □ Have rollback plan ready                                         │
│  □ Test in staging first                                            │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Memory Considerations

```
Before removing node:
┌─────────┬─────────┬─────────┬─────────┐
│  25 GB  │  25 GB  │  25 GB  │  25 GB  │  = 100 GB total data
└─────────┴─────────┴─────────┴─────────┘
  4 nodes × 32 GB RAM = 128 GB capacity (78% used)

After removing node:
┌──────────┬──────────┬──────────┐
│  33.3 GB │  33.3 GB │  33.3 GB │  = 100 GB total data
└──────────┴──────────┴──────────┘
  3 nodes × 32 GB RAM = 96 GB capacity (104% used!) ← PROBLEM!

⚠️  Always verify remaining nodes can hold the data!
```

### Recommended Order

1. **Check capacity** - Can remaining nodes hold all data?
2. **Remove replicas first** - Simplest, no data movement
3. **Reshard in batches** - Don't move all slots at once
4. **Monitor throughout** - Watch cluster state and latency
5. **Verify after** - Check slot distribution and health

---

## 16. Summary & Commands

### Process Summary

```
For Master Node:
1. Remove its replica      → del-node <replica-id>
2. Reshard slots away      → reshard --cluster-from <master-id>
3. Remove empty master     → del-node <master-id>
4. Stop container          → docker stop <container>

For Replica Node:
1. Remove from cluster     → del-node <replica-id>
2. Stop container          → docker stop <container>
```

### Commands Reference

| Command | Purpose |
|---------|---------|
| `make scale-down` | Remove node and rebalance |
| `redis-cli --cluster reshard` | Move slots between nodes |
| `redis-cli --cluster del-node` | Remove node from cluster |
| `redis-cli --cluster rebalance` | Auto-distribute slots evenly |
| `redis-cli --cluster fix` | Fix stuck migrations |
| `CLUSTER FORGET <id>` | Manually forget a node |

### Key Concepts

| Concept | Description |
|---------|-------------|
| **Reshard** | Move slots from one node to another |
| **Del-node** | Remove empty node from cluster |
| **CLUSTER FORGET** | Make node forget about another node |
| **Zero-downtime** | Cluster stays operational throughout |
| **Capacity planning** | Ensure remaining nodes can hold data |

---

## 17. Comparison: Scale Up vs Scale Down

```
┌─────────────────────────────────────────────────────────────────────┐
│              SCALE UP vs SCALE DOWN                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Scale UP (Add Node):          Scale DOWN (Remove Node):            │
│  ────────────────────          ──────────────────────────           │
│  1. Start container            1. Remove replica (if any)           │
│  2. Add to cluster             2. Reshard slots AWAY                │
│  3. Rebalance (slots TO new)   3. Remove empty node                 │
│  4. Add replica                4. Stop container                    │
│                                                                     │
│  Slots flow: Others → New      Slots flow: Leaving → Others         │
│  Result: More capacity         Result: Consolidated capacity        │
│  Risk: Low                     Risk: Medium (capacity check!)       │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Next Lab

**Lab 7: Failure Recovery** - Learn how to recover from catastrophic failures and restore cluster from backups.

```bash
# Preview Lab 7
make backup
make simulate-disaster
make restore
```
