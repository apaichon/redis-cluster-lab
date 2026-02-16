# Exercise 5: Automatic Failover - Detailed Guide

## Overview

This exercise demonstrates Redis Cluster's automatic failover capability - one of its most powerful features for high availability. When a master node fails, the cluster automatically promotes a replica to become the new master, ensuring continuous operation without manual intervention.

---

## Learning Objectives

By the end of this exercise, you will understand:
1. How Redis Cluster detects node failures
2. The failover election process
3. How replicas get promoted to masters
4. How the cluster maintains data availability
5. How recovered nodes rejoin the cluster

---

## Prerequisites

- Completed Exercise 1-4
- Running Redis Cluster with 3 masters and 3 replicas
- Two terminal windows available

---

## Architecture Before Failover

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     INITIAL CLUSTER STATE                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   MASTER 1      │  │   MASTER 2      │  │   MASTER 3      │          │
│  │   redis-1       │  │   redis-2       │  │   redis-3       │          │
│  │   Port: 7001    │  │   Port: 7002    │  │   Port: 7003    │          │
│  │                 │  │                 │  │                 │          │
│  │  Slots: 0-5460  │  │ Slots: 5461-10922│ │Slots: 10923-16383│         │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘          │
│           │                    │                    │                   │
│           │ replication        │ replication        │ replication       │
│           ▼                    ▼                    ▼                   │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   REPLICA 1     │  │   REPLICA 2     │  │   REPLICA 3     │          │
│  │   redis-4       │  │   redis-5       │  │   redis-6       │          │
│  │   Port: 7004    │  │   Port: 7005    │  │   Port: 7006    │          │
│  │                 │  │                 │  │                 │          │
│  │  (synced copy   │  │  (synced copy   │  │  (synced copy   │          │
│  │   of Master 1)  │  │   of Master 2)  │  │   of Master 3)  │          │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘          │
│                                                                         │
│  Data Flow:                                                             │
│  • Writes go to Masters only                                            │
│  • Masters replicate data to their Replicas                             │
│  • Replicas are read-only hot standbys                                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Step 1: Understand the Current Cluster State

### 1.1 Check Cluster Health

First, let's verify our cluster is healthy before we start:

```bash
make cluster-info
```

**Expected Output:**
```
Cluster Nodes:
abc123... 172.30.0.11:7001@17001 myself,master - 0 0 1 connected 0-5460
def456... 172.30.0.12:7002@17002 master - 0 1705312345 2 connected 5461-10922
ghi789... 172.30.0.13:7003@17003 master - 0 1705312346 3 connected 10923-16383
jkl012... 172.30.0.14:7004@17004 slave abc123... 0 1705312347 4 connected
mno345... 172.30.0.15:7005@17005 slave def456... 0 1705312348 5 connected
pqr678... 172.30.0.16:7006@17006 slave ghi789... 0 1705312349 6 connected

Cluster Info:
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
cluster_size:3
```

**Understanding the Output:**

| Field | Meaning |
|-------|---------|
| `myself,master` | This node is a master (from redis-1's perspective) |
| `slave abc123...` | This replica belongs to master with ID abc123... |
| `connected` | Node is reachable and communicating |
| `0-5460` | Slot range this master is responsible for |
| `cluster_state:ok` | Cluster is healthy and operational |
| `cluster_size:3` | 3 master nodes (shards) |

### 1.2 Identify Master-Replica Pairs

Let's clearly identify which replica belongs to which master:

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep -E "master|slave"
```

**Parse the relationships:**
```
# Master 1 (redis-1, port 7001) → Replica 4 (redis-4, port 7004)
# Master 2 (redis-2, port 7002) → Replica 5 (redis-5, port 7005)
# Master 3 (redis-3, port 7003) → Replica 6 (redis-6, port 7006)
```

### 1.3 Create Test Data on Master 1

Let's create some data that lives on Master 1 (slots 0-5460):

```bash
# Connect to the cluster and create test data
docker exec redis-1 redis-cli -c -p 7001 << 'EOF'
# Create a key that hashes to Master 1's slot range
# We use hash tag {test1} to control slot placement
SET {test1}:name "Failover Test Data"
SET {test1}:value "12345"
SET {test1}:timestamp "2024-01-15T10:00:00Z"
HSET {test1}:info created_by "admin" purpose "failover testing"
EOF
```

**Verify the data and its location:**

```bash
# Check which slot these keys belong to
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "{test1}:name"
```

**Output:** `(integer) 4768` - This slot belongs to Master 1 (slots 0-5460)

```bash
# Verify data exists
docker exec redis-1 redis-cli -c -p 7001 GET {test1}:name
```

**Output:** `"Failover Test Data"`

---

## Step 2: Set Up Monitoring (Terminal 1)

### 2.1 Start Cluster Watcher

In your **first terminal**, start the cluster monitoring:

```bash
make watch-cluster
```

**What this command does:**

```makefile
# From Makefile

watch-cluster:
	@echo "Watching cluster state (Ctrl+C to stop)..."
	@while true; do \
		clear; \
		echo "=== Redis Cluster Status (updated every 2s) ==="; \
		echo ""; \
		docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null || docker exec redis-2 redis-cli -p 7002 cluster nodes 2>/dev/null || echo "Cannot connect to cluster"; \
		echo ""; \
		echo "--- Cluster Info ---"; \
		docker exec redis-1 redis-cli -p 7001 cluster info 2>/dev/null | grep -E "cluster_state|cluster_slots_ok|cluster_known_nodes" || \
		docker exec redis-2 redis-cli -p 7002 cluster info 2>/dev/null | grep -E "cluster_state|cluster_slots_ok|cluster_known_nodes"; \
		sleep 2; \
	done
```

**Explanation:**
1. Runs in an infinite loop
2. Clears screen every iteration
3. Queries `CLUSTER NODES` to show all node statuses
4. Queries `CLUSTER INFO` for overall cluster health
5. Has fallback to other nodes if redis-1 is down
6. Refreshes every 2 seconds

**Initial display should show:**
```
=== Redis Cluster Status (updated every 2s) ===
abc123... 172.30.0.11:7001@17001 myself,master - 0 0 1 connected 0-5460
def456... 172.30.0.12:7002@17002 master - 0 1705312345 2 connected 5461-10922
ghi789... 172.30.0.13:7003@17003 master - 0 1705312346 3 connected 10923-16383
jkl012... 172.30.0.14:7004@17004 slave abc123... 0 1705312347 4 connected
mno345... 172.30.0.15:7005@17005 slave def456... 0 1705312348 5 connected
pqr678... 172.30.0.16:7006@17006 slave ghi789... 0 1705312349 6 connected

=== Cluster Info ===
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
```

**Keep this terminal open and visible!**

---

## Step 3: Trigger Failover (Terminal 2)

### 3.1 Open Second Terminal

Open a **second terminal** and navigate to the project directory:

```bash
cd /path/to/cluster-labs
```

### 3.2 Execute Failover Command

```bash
make failover
```

**What this command does:**

```makefile
# From Makefile
failover:
	@echo "Simulating master failure (stopping redis-1)..."
	docker stop redis-1
	@echo ""
	@echo "Watch the cluster state with 'make watch-cluster'"
	@echo "The replica should be promoted to master within ~5-10 seconds"
	@echo ""
	@echo "To recover: make recover"
```

**Explanation:**
1. Stops the redis-1 container (Master 1)
2. This simulates a sudden master failure (crash, network partition, hardware failure)
3. The container process is killed immediately

**Alternative - Manual approach:**

```bash
# You can also stop the container directly:
docker stop redis-1

# Or simulate a crash (more abrupt):
docker kill redis-1

# Or pause (simulates network partition):
docker pause redis-1
```

---

## Step 4: Observe the Failover Process

### 4.1 Failover Timeline

Watch Terminal 1 carefully. You'll see the following progression:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     FAILOVER TIMELINE                                   │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Time 0s: Master 1 Stops                                                │
│  ─────────────────────────────────────────────────────────────────────  │
│  • docker stop redis-1 executed                                         │
│  • Master 1 immediately stops responding                                │
│  • No changes visible in cluster state yet                              │
│                                                                         │
│  Time 1-2s: Failure Detection Begins                                    │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica 4 notices Master 1 not responding to heartbeats              │
│  • Other masters also detect Master 1 is unreachable                    │
│  • Gossip protocol spreads the information                              │
│                                                                         │
│  Time 2-4s: PFAIL Status                                                │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Nodes mark Master 1 as PFAIL (Potentially Failed)                    │
│  • This is a subjective opinion from individual nodes                   │
│  • You'll see in cluster nodes output:                                  │
│    abc123... 172.30.0.11:7001@17001 master,pfail - ...                  │
│                                                                         │
│  Time 5s: FAIL Status (cluster-node-timeout reached)                    │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Majority of masters agree: Master 1 is FAIL                          │
│  • This is objective consensus                                          │
│  • You'll see:                                                          │
│    abc123... 172.30.0.11:7001@17001 master,fail - ...                   │
│                                                                         │
│  Time 5-6s: Failover Election                                           │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica 4 starts election process                                    │
│  • Requests votes from other masters                                    │
│  • Other masters vote (each master gets one vote per epoch)             │
│                                                                         │
│  Time 6s: Replica Promotion                                             │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica 4 wins election (receives majority votes)                    │
│  • Replica 4 becomes Master 4                                           │
│  • Takes over slots 0-5460                                              │
│  • Announces new role to all nodes                                      │
│  • You'll see:                                                          │
│    jkl012... 172.30.0.14:7004@17004 master - ... connected 0-5460       │
│                                                                         │
│  Time 6s+: Cluster Operational                                          │
│  ─────────────────────────────────────────────────────────────────────  │
│  • cluster_state returns to "ok"                                        │
│  • All 16384 slots are covered                                          │
│  • Clients automatically redirected to new master                       │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 Watch Terminal Output Changes

**Stage 1: Initial failure (0-2 seconds)**
```
=== Cluster Nodes ===
abc123... 172.30.0.11:7001@17001 master,fail? - 1705312400 0 1 disconnected 0-5460
                                        ^^^^^ PFAIL suspected
def456... 172.30.0.12:7002@17002 master - 0 1705312401 2 connected 5461-10922
ghi789... 172.30.0.13:7003@17003 master - 0 1705312402 3 connected 10923-16383
jkl012... 172.30.0.14:7004@17004 slave abc123... 0 1705312403 4 connected
                                 ^^^^^ Still showing as slave
...

=== Cluster Info ===
cluster_state:fail          <-- Cluster is unhealthy!
cluster_slots_assigned:16384
cluster_slots_ok:10923      <-- Only ~11000 slots working (5461 slots down)
cluster_slots_pfail:5461    <-- These slots are potentially failed
cluster_known_nodes:6
```

**Stage 2: Confirmed failure (5 seconds)**
```
=== Cluster Nodes ===
abc123... 172.30.0.11:7001@17001 master,fail - 1705312400 0 1 disconnected 0-5460
                                        ^^^^ Confirmed FAIL
...
```

**Stage 3: After promotion (6+ seconds)**
```
=== Cluster Nodes ===
abc123... 172.30.0.11:7001@17001 master,fail - 1705312400 0 1 disconnected
                                                               ^^^^^^^^^^^^ No slots now
jkl012... 172.30.0.14:7004@17004 master - 0 1705312410 7 connected 0-5460
                                 ^^^^^^                            ^^^^^^^
                                 Now MASTER                        Has the slots!
def456... 172.30.0.12:7002@17002 master - 0 1705312411 2 connected 5461-10922
ghi789... 172.30.0.13:7003@17003 master - 0 1705312412 3 connected 10923-16383
mno345... 172.30.0.15:7005@17005 slave def456... 0 1705312413 5 connected
pqr678... 172.30.0.16:7006@17006 slave ghi789... 0 1705312414 6 connected

=== Cluster Info ===
cluster_state:ok            <-- Cluster is healthy again!
cluster_slots_assigned:16384
cluster_slots_ok:16384      <-- All slots operational
cluster_slots_pfail:0
cluster_known_nodes:6       <-- Still knows about failed node
```

---

## Step 5: Verify Data Integrity

### 5.1 Check Test Data Still Exists

The data we created on Master 1 should now be accessible via the new Master 4:

```bash
# Try to get data through any cluster node
docker exec redis-2 redis-cli -c -p 7002 GET {test1}:name
```

**Expected Output:** `"Failover Test Data"`

**What happens internally:**
1. Client connects to redis-2 (Master 2)
2. Redis calculates slot for `{test1}:name` → slot 4768
3. Slot 4768 now belongs to Master 4 (redis-4, port 7004)
4. Redis returns `MOVED 4768 172.30.0.14:7004`
5. Client automatically redirects to redis-4
6. Data is returned successfully

### 5.2 Verify Through New Master Directly

```bash
# Connect directly to the new master
docker exec redis-4 redis-cli -p 7004 GET {test1}:name
```

**Expected Output:** `"Failover Test Data"`

### 5.3 Check All Test Data

```bash
docker exec redis-4 redis-cli -p 7004 << 'EOF'
GET {test1}:name
GET {test1}:value
GET {test1}:timestamp
HGETALL {test1}:info
EOF
```

**Expected Output:**
```
"Failover Test Data"
"12345"
"2024-01-15T10:00:00Z"
1) "created_by"
2) "admin"
3) "purpose"
4) "failover testing"
```

**Why the data is preserved:**
- Replicas continuously sync data from their masters
- Before failure, Replica 4 had an exact copy of Master 1's data
- When promoted, it already has all the data
- No data loss occurs (assuming replication was caught up)

---

## Step 6: Understand the New Cluster State

### 6.1 New Topology

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     CLUSTER STATE AFTER FAILOVER                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   (FAILED)      │  │   MASTER 2      │  │   MASTER 3      │          │
│  │   redis-1       │  │   redis-2       │  │   redis-3       │          │
│  │   Port: 7001    │  │   Port: 7002    │  │   Port: 7003    │          │
│  │                 │  │                 │  │                 │          │
│  │   DOWN ✗        │  │ Slots: 5461-10922│ │Slots: 10923-16383│         │
│  └─────────────────┘  └────────┬────────┘  └────────┬────────┘          │
│                                │                    │                   │
│                                │ replication        │ replication       │
│                                ▼                    ▼                   │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   NEW MASTER 4  │  │   REPLICA 2     │  │   REPLICA 3     │          │
│  │   redis-4       │  │   redis-5       │  │   redis-6       │          │
│  │   Port: 7004    │  │   Port: 7005    │  │   Port: 7006    │          │
│  │                 │  │                 │  │                 │          │
│  │  Slots: 0-5460  │  │  (synced copy   │  │  (synced copy   │          │
│  │  PROMOTED! ★    │  │   of Master 2)  │  │   of Master 3)  │          │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘          │
│                                                                         │
│  WARNING: Master 4 has NO REPLICA now!                                  │
│  If Master 4 fails, data in slots 0-5460 will be LOST!                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 6.2 Current Risk Assessment

```bash
# Check replica count for each master
docker exec redis-2 redis-cli -p 7002 CLUSTER NODES | grep master
```

**Output analysis:**
```
jkl012... 172.30.0.14:7004@17004 master - ... connected 0-5460      # NO REPLICA!
def456... 172.30.0.12:7002@17002 master - ... connected 5461-10922  # Has redis-5
ghi789... 172.30.0.13:7003@17003 master - ... connected 10923-16383 # Has redis-6
```

**Risk:** The new Master 4 (redis-4) has no replica. If it fails now, data loss will occur!

---

## Step 7: Recover the Failed Node

### 7.1 Execute Recovery

```bash
make recover
```

**What this command does:**

```makefile
# From Makefile
recover:
	@echo "Recovering redis-1..."
	docker start redis-1
	@echo ""
	@echo "redis-1 will rejoin as a replica of the new master"
	@echo "Check with 'make cluster-info'"
```

**Explanation:**
1. Starts the redis-1 container again
2. Redis server boots up and reads its cluster configuration
3. Discovers the cluster topology has changed
4. Automatically becomes a replica of the new master (redis-4)

### 7.2 Watch the Recovery

In Terminal 1 (watch-cluster), observe:

**During recovery:**
```
=== Cluster Nodes ===
abc123... 172.30.0.11:7001@17001 slave jkl012... 0 1705312500 1 connected
                                 ^^^^^
                                 Now a SLAVE (replica) of redis-4!

jkl012... 172.30.0.14:7004@17004 master - 0 1705312501 7 connected 0-5460
def456... 172.30.0.12:7002@17002 master - 0 1705312502 2 connected 5461-10922
ghi789... 172.30.0.13:7003@17003 master - 0 1705312503 3 connected 10923-16383
mno345... 172.30.0.15:7005@17005 slave def456... 0 1705312504 5 connected
pqr678... 172.30.0.16:7006@17006 slave ghi789... 0 1705312505 6 connected

=== Cluster Info ===
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
```

### 7.3 New Topology After Recovery

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     CLUSTER STATE AFTER RECOVERY                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   MASTER 4      │  │   MASTER 2      │  │   MASTER 3      │          │
│  │   redis-4       │  │   redis-2       │  │   redis-3       │          │
│  │   Port: 7004    │  │   Port: 7002    │  │   Port: 7003    │          │
│  │                 │  │                 │  │                 │          │
│  │  Slots: 0-5460  │  │ Slots: 5461-10922│ │Slots: 10923-16383│         │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘          │
│           │                    │                    │                   │
│           │ replication        │ replication        │ replication       │
│           ▼                    ▼                    ▼                   │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │   REPLICA 1     │  │   REPLICA 2     │  │   REPLICA 3     │          │
│  │   redis-1       │  │   redis-5       │  │   redis-6       │          │
│  │   Port: 7001    │  │   Port: 7005    │  │   Port: 7006    │          │
│  │                 │  │                 │  │                 │          │
│  │  RECOVERED!     │  │  (synced copy   │  │  (synced copy   │          │
│  │  Now replica    │  │   of Master 2)  │  │   of Master 3)  │          │
│  │  of Master 4    │  │                 │  │                 │          │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘          │
│                                                                         │
│  ✓ Full redundancy restored!                                            │
│  ✓ Each master has one replica                                          │
│  ✓ Data is safe again                                                   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Step 8: Verify Recovery

### 8.1 Check Replication Status

```bash
# Check redis-1's new role
docker exec redis-1 redis-cli -p 7001 ROLE
```

**Expected Output:**
```
1) "slave"
2) "172.30.0.14"    # Master's IP (redis-4)
3) (integer) 7004   # Master's port
4) "connected"      # Replication state
5) (integer) 123456 # Replication offset
```

### 8.2 Check from Master's Perspective

```bash
# Check redis-4's replication info
docker exec redis-4 redis-cli -p 7004 INFO replication
```

**Expected Output:**
```
# Replication
role:master
connected_slaves:1
slave0:ip=172.30.0.11,port=7001,state=online,offset=123456,lag=0
master_replid:abc123def456...
master_repl_offset:123456
```

**Field meanings:**
| Field | Meaning |
|-------|---------|
| `role:master` | This node is a master |
| `connected_slaves:1` | One replica connected |
| `slave0:...state=online` | Replica is healthy and syncing |
| `offset=123456` | Replication progress (should match master) |
| `lag=0` | No replication lag (fully synced) |

### 8.3 Verify Data Sync

```bash
# Write new data to master
docker exec redis-4 redis-cli -p 7004 SET "{test1}:after_recovery" "new data"

# Read from replica (should have it)
docker exec redis-1 redis-cli -p 7001 GET "{test1}:after_recovery"
```

**Expected Output:** `"new data"`

---

## Deep Dive: The Failover Algorithm

### Failure Detection Process

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    FAILURE DETECTION ALGORITHM                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  1. HEARTBEAT MECHANISM                                                 │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Every node sends PING to other nodes periodically                    │
│  • Expected PONG response within cluster-node-timeout (default 5000ms)  │
│  • Uses Cluster Bus (port 17001-17006) for communication                │
│                                                                         │
│  2. PFAIL (Potentially Failed)                                          │
│  ─────────────────────────────────────────────────────────────────────  │
│  • If node A doesn't receive PONG from node B within timeout            │
│  • Node A marks B as PFAIL (subjective opinion)                         │
│  • PFAIL is local to each node, not cluster-wide consensus              │
│                                                                         │
│  3. GOSSIP PROTOCOL                                                     │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Nodes share their PFAIL flags via gossip messages                    │
│  • Each PING/PONG carries information about other nodes                 │
│  • Eventually all nodes learn about the PFAIL suspicion                 │
│                                                                         │
│  4. FAIL (Confirmed Failed)                                             │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Requires MAJORITY of masters to agree on PFAIL                       │
│  • With 3 masters: need 2 to agree (3/2 + 1 = 2)                        │
│  • Once majority agrees, node is marked as FAIL                         │
│  • FAIL is broadcast to all nodes                                       │
│                                                                         │
│  5. FAILOVER TRIGGER                                                    │
│  ─────────────────────────────────────────────────────────────────────  │
│  • When master is marked FAIL, its replicas become eligible             │
│  • Eligible replica waits a small delay (based on rank)                 │
│  • Then starts election process                                         │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Election Process

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    REPLICA ELECTION PROCESS                             │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  1. ELECTION DELAY                                                      │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica waits: DELAY = 500ms + random(0-500ms) + RANK * 1000ms       │
│  • RANK is based on replication offset (most up-to-date = rank 0)       │
│  • This ensures most up-to-date replica has priority                    │
│                                                                         │
│  2. REQUEST VOTES                                                       │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica increments currentEpoch                                      │
│  • Sends FAILOVER_AUTH_REQUEST to all masters                           │
│  • Request includes replica's current replication offset                │
│                                                                         │
│  3. VOTE CASTING                                                        │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Each master can vote once per epoch                                  │
│  • Master votes if:                                                     │
│    - The failed master's slots need coverage                            │
│    - Replica's epoch >= master's current epoch                          │
│    - Master hasn't voted for another replica this epoch                 │
│  • Sends FAILOVER_AUTH_ACK if voting yes                                │
│                                                                         │
│  4. WIN ELECTION                                                        │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica needs majority of master votes                               │
│  • With 3 masters: needs 2 votes (quorum)                               │
│  • If achieved: replica promotes itself                                 │
│  • If not: waits for next election attempt                              │
│                                                                         │
│  5. PROMOTION                                                           │
│  ─────────────────────────────────────────────────────────────────────  │
│  • Replica changes its state to master                                  │
│  • Takes over failed master's slot range                                │
│  • Broadcasts PONG with new configuration                               │
│  • Other nodes update their routing tables                              │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Configuration Parameters

### Key Failover Parameters

```conf
# redis.conf

# Time to wait before considering a node failed
cluster-node-timeout 5000

# If set to yes, replica won't failover if data is too old
cluster-replica-validity-factor 10

# Minimum number of replicas that must be reachable
# for master to accept writes (0 = disabled)
cluster-require-full-coverage yes

# Allow reads from replicas (useful during failover)
replica-read-only yes
```

**Parameter explanations:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `cluster-node-timeout` | 5000ms | Time before PFAIL → FAIL |
| `cluster-replica-validity-factor` | 10 | Max replication lag multiplier |
| `cluster-require-full-coverage` | yes | Whether cluster requires all slots covered |

### Calculating Failover Time

```
Minimum failover time ≈ cluster-node-timeout + election_delay + propagation
                     ≈ 5000ms + ~500ms + ~500ms
                     ≈ 6 seconds

Maximum failover time ≈ cluster-node-timeout + max_election_delay + retries
                     ≈ 5000ms + 2000ms + potential_retries
                     ≈ 7-15 seconds
```

---

## Troubleshooting

### Problem: Failover Not Happening

**Possible causes:**

1. **Not enough masters for quorum**
```bash
# Check how many masters are up
docker exec redis-2 redis-cli -p 7002 CLUSTER NODES | grep master | grep -v fail | wc -l
```
Need at least 2 masters for a 3-master cluster.

2. **Replica not eligible**
```bash
# Check replica's replication status
docker exec redis-4 redis-cli -p 7004 INFO replication
```
Look for `master_link_status:down` or large `master_repl_offset` difference.

3. **cluster-require-full-coverage preventing operations**
```bash
docker exec redis-2 redis-cli -p 7002 CONFIG GET cluster-require-full-coverage
```

### Problem: Data Loss After Failover

**Possible cause:** Replica was behind in replication

```bash
# Before failover, check replication lag
docker exec redis-1 redis-cli -p 7001 INFO replication | grep lag
```

If `slave_repl_offset` is much smaller than `master_repl_offset`, some data might be lost.

### Problem: Node Won't Rejoin

**Reset the node if needed:**
```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER RESET SOFT
```

Then manually join:
```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER MEET 172.30.0.14 7004
```

---

## Summary

### What We Learned

1. **Automatic Detection**: Cluster detects failures through heartbeat timeouts and gossip protocol

2. **Consensus-Based**: Requires majority of masters to agree before failover

3. **Automatic Promotion**: Replicas automatically promote themselves when master fails

4. **Data Preservation**: Data is preserved because replicas have synchronized copies

5. **Automatic Recovery**: Failed nodes automatically rejoin as replicas

### Key Metrics

| Metric | Typical Value |
|--------|---------------|
| Detection time | 5 seconds (cluster-node-timeout) |
| Total failover time | 6-10 seconds |
| Data loss | Zero (if replication was caught up) |
| Manual intervention required | None |

### Best Practices

1. **Always have replicas**: Each master should have at least one replica
2. **Monitor replication lag**: Ensure replicas are caught up before failures
3. **Test failover regularly**: Verify your cluster can handle failures
4. **Consider cluster-node-timeout**: Lower = faster failover, but more false positives

---

## Next Steps

After completing this exercise:

1. **Exercise 6**: Manual failover (controlled switchover)
2. **Exercise 7**: Scaling down (removing nodes safely)
3. **Exercise 8**: Load testing during failover

---

## Clean Up

If you want to restore the original master-replica relationships:

```bash
# Stop all and restart fresh
make clean
make start
```

Or perform a manual failover to restore redis-1 as master:

```bash
# Force redis-1 to become master again
docker exec redis-1 redis-cli -p 7001 CLUSTER FAILOVER TAKEOVER
```

**Note:** `CLUSTER FAILOVER TAKEOVER` forces immediate promotion without waiting for election.
