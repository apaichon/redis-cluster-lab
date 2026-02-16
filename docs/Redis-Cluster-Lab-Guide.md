# Redis Cluster - Step-by-Step Lab Guide

## 🎯 Workshop Format: Hands-On Labs

**Duration:** 3 hours
**Format:** Instructor-led with hands-on exercises
**Setup:** Each participant needs Docker and terminal access

---

## 📋 Prerequisites Check (5 minutes)

### Step 1: Verify Docker Installation

```bash
docker --version
# Expected: Docker version 20.10+ or higher
```

### Step 2: Verify Docker Compose

```bash
docker compose version
# Expected: Docker Compose version v2.0+ or higher
```

### Step 3: Clone or Navigate to Project

```bash
cd /path/to/redis-cluster-lab
```

### Step 4: Verify Make is Available

```bash
make --version
# Expected: GNU Make 3.81 or higher
```

✅ **Checkpoint:** All commands above should return version information without errors.

---

## Lab 1: Initialize Redis Cluster (30 minutes)

### 🎓 Learning Objectives
- Start a 6-node Redis Cluster
- Understand cluster topology
- Verify cluster health
- Explore cluster configuration

---

### Exercise 1.1: Start the Cluster (10 minutes)

#### Step 1: Clean Previous State (if any)

```bash
make clean
```

**Expected Output:**
```
Stopping containers...
Removing containers and volumes...
Done.
```

#### Step 2: Start the Cluster

```bash
make start
```

**Expected Output:**
```
Starting Redis cluster (6 nodes)...
[+] Running 6/6
 ✔ Container redis-1  Started
 ✔ Container redis-2  Started
 ✔ Container redis-3  Started
 ✔ Container redis-4  Started
 ✔ Container redis-5  Started
 ✔ Container redis-6  Started
Initializing cluster...
>>> Performing hash slots allocation on 6 nodes...
Master[0] -> Slots 0 - 5460
Master[1] -> Slots 5461 - 10922
Master[2] -> Slots 10923 - 16383
...
[OK] All 16384 slots covered.
```

#### Step 3: Verify Containers are Running

```bash
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
```

**Expected Output:**
```
NAMES     STATUS          PORTS
redis-1   Up 10 seconds   0.0.0.0:7001->7001/tcp, 0.0.0.0:17001->17001/tcp
redis-2   Up 10 seconds   0.0.0.0:7002->7002/tcp, 0.0.0.0:17002->17002/tcp
redis-3   Up 10 seconds   0.0.0.0:7003->7003/tcp, 0.0.0.0:17003->17003/tcp
redis-4   Up 10 seconds   0.0.0.0:7004->7004/tcp, 0.0.0.0:17004->17004/tcp
redis-5   Up 10 seconds   0.0.0.0:7005->7005/tcp, 0.0.0.0:17005->17005/tcp
redis-6   Up 10 seconds   0.0.0.0:7006->7006/tcp, 0.0.0.0:17006->17006/tcp
```

✅ **Checkpoint:** All 6 containers should be running with status "Up".

---

### Exercise 1.2: Verify Cluster State (10 minutes)

#### Step 1: Check Cluster Info

```bash
make cluster-info
```

**Expected Output:**
```
Cluster Nodes:
<node-id-1> 172.30.0.11:7001@17001 myself,master - 0 0 1 connected 0-5460
<node-id-2> 172.30.0.12:7002@17002 master - 0 1234567890 2 connected 5461-10922
<node-id-3> 172.30.0.13:7003@17003 master - 0 1234567891 3 connected 10923-16383
<node-id-4> 172.30.0.14:7004@17004 slave <node-id-1> 0 1234567892 4 connected
<node-id-5> 172.30.0.15:7005@17005 slave <node-id-2> 0 1234567893 5 connected
<node-id-6> 172.30.0.16:7006@17006 slave <node-id-3> 0 1234567894 6 connected

Cluster Info:
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
cluster_size:3
```

#### Step 2: View Slot Distribution

```bash
make slot-info
```

**Expected Output:**
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

#### Step 3: Check Individual Node Role

```bash
docker exec redis-1 redis-cli -p 7001 ROLE
```

**Expected Output (Master):**
```
1) "master"
2) (integer) 1234567890
3) 1) 1) "172.30.0.14"
      2) "7004"
      3) "1234567890"
```

```bash
docker exec redis-4 redis-cli -p 7004 ROLE
```

**Expected Output (Replica):**
```
1) "slave"
2) "172.30.0.11"
3) (integer) 7001
4) "connected"
5) (integer) 1234567890
```

✅ **Checkpoint:**
- cluster_state should be "ok"
- All 16,384 slots should be assigned
- 3 masters and 3 replicas should be visible

---

### Exercise 1.3: Basic Cluster Operations (10 minutes)

#### Step 1: Set a Key

```bash
docker exec redis-1 redis-cli -c -p 7001 SET mykey "Hello Redis Cluster"
```

**Expected Output:**
```
OK
```

**Note:** The `-c` flag enables cluster mode in redis-cli (handles redirects).

#### Step 2: Get the Key from Different Node

```bash
docker exec redis-2 redis-cli -c -p 7002 GET mykey
```

**Expected Output:**
```
-> Redirected to slot [14687] located at 172.30.0.13:7003
"Hello Redis Cluster"
```

**Observation:** Key was stored on Master 3 (slot 14687), and redis-cli automatically redirected.

#### Step 3: Find Which Slot a Key Uses

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT mykey
```

**Expected Output:**
```
(integer) 14687
```

#### Step 4: Check Which Keys are in a Slot

```bash
docker exec redis-3 redis-cli -p 7003 CLUSTER GETKEYSINSLOT 14687 10
```

**Expected Output:**
```
1) "mykey"
```

✅ **Checkpoint:** You should be able to set and retrieve keys from any node in the cluster.

---

### 📝 Lab 1 Summary

**What You Did:**
- ✅ Started 6-node Redis Cluster (3 masters + 3 replicas)
- ✅ Verified cluster health and slot distribution
- ✅ Understood cluster topology
- ✅ Performed basic read/write operations

**Key Takeaways:**
- Redis Cluster uses 16,384 hash slots
- Slots are distributed across master nodes
- Replicas provide high availability
- Cluster handles redirects automatically

---

## Lab 2: Hash Slots & Key Distribution (35 minutes)

### 🎓 Learning Objectives
- Understand CRC16 hash slot calculation
- Learn hash tag syntax
- Solve cross-slot operation challenges
- Design key patterns for atomic operations

---

### Exercise 2.1: Understanding Slot Calculation (10 minutes)

#### Step 1: Calculate Slots for Multiple Keys

```bash
# Check slot for different keys
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "user:1"
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "user:2"
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "user:3"
```

**Example Output:**
```
(integer) 9842   # user:1 -> slot 9842 (Master 2)
(integer) 2881   # user:2 -> slot 2881 (Master 1)
(integer) 5831   # user:3 -> slot 5831 (Master 2)
```

**Observation:** Different keys hash to different slots, distributing data across nodes.

#### Step 2: Use the Helper Command

```bash
make key-slot KEY="user:1001"
```

**Expected Output:**
```
┌──────────────────────────────────────────────────────────────────┐
│                    KEY TO SLOT MAPPING                           │
├──────────────────────────────────────────────────────────────────┤
│  Key: user:1001                                                  │
│  Slot: 12539                                                     │
│  Node: 172.30.0.13:7003                                          │
└──────────────────────────────────────────────────────────────────┘
```

#### Step 3: Test Multiple Related Keys

```bash
# Without hash tags - different slots
make key-slot KEY="event:123"
make key-slot KEY="event:123:seats"
make key-slot KEY="event:123:waitlist"
```

**Expected Output:**
```
event:123          → Slot 10456
event:123:seats    → Slot 3892
event:123:waitlist → Slot 15234
```

**Problem:** Related event data scattered across 3 different nodes!

✅ **Checkpoint:** You should see that related keys without hash tags end up on different slots.

---

### Exercise 2.2: Hash Tags for Key Co-location (15 minutes)

#### Step 1: Test Hash Tags

```bash
# With hash tags - same slot
make key-slot KEY="{event:123}"
make key-slot KEY="{event:123}:seats"
make key-slot KEY="{event:123}:waitlist"
```

**Expected Output:**
```
{event:123}           → Slot 10456
{event:123}:seats     → Slot 10456
{event:123}:waitlist  → Slot 10456
```

**Solution:** All keys on the SAME slot!

#### Step 2: Run Hash Tag Demo

```bash
make hash-tag-demo
```

**Expected Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║                   HASH TAG DEMONSTRATION                         ║
╠══════════════════════════════════════════════════════════════════╣

┌── WITHOUT HASH TAGS ──────────────────────────────────────────────┐
│  user:1001             → Slot 12539 → 172.30.0.13:7003
│  user:1002             → Slot  5649 → 172.30.0.12:7002
│  user:1003             → Slot  1440 → 172.30.0.11:7001
│  (Keys distributed across different slots/nodes)
└───────────────────────────────────────────────────────────────────┘

┌── WITH HASH TAGS (Same user) ────────────────────────────────────┐
│  {user:1001}:profile   → Slot 12539 → 172.30.0.13:7003
│  {user:1001}:orders    → Slot 12539 → 172.30.0.13:7003
│  {user:1001}:cart      → Slot 12539 → 172.30.0.13:7003
│  (All keys on SAME slot - can use MULTI/Lua together!)
└───────────────────────────────────────────────────────────────────┘
```

#### Step 3: Try Cross-Slot Operations

```bash
# This will FAIL - different slots
docker exec redis-1 redis-cli -c -p 7001 MSET user:1 "Alice" user:2 "Bob" user:3 "Charlie"
```

**Expected Output:**
```
(error) CROSSSLOT Keys in request don't hash to the same slot
```

#### Step 4: Fix with Hash Tags

```bash
# This WORKS - same slot
docker exec redis-1 redis-cli -c -p 7001 MSET \
    "{user:1}:name" "Alice" \
    "{user:1}:email" "alice@test.com" \
    "{user:1}:age" "30"
```

**Expected Output:**
```
OK
```

#### Step 5: Retrieve All Keys Atomically

```bash
docker exec redis-1 redis-cli -c -p 7001 MGET \
    "{user:1}:name" \
    "{user:1}:email" \
    "{user:1}:age"
```

**Expected Output:**
```
1) "Alice"
2) "alice@test.com"
3) "30"
```

✅ **Checkpoint:** You should be able to perform multi-key operations on keys with the same hash tag.

---

### Exercise 2.3: Cross-Slot Demo (10 minutes)

#### Step 1: Run Cross-Slot Demo

```bash
make cross-slot-demo
```

**Expected Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              CROSS-SLOT OPERATIONS DEMONSTRATION                  ║
╚══════════════════════════════════════════════════════════════════╝

┌── LUA SCRIPT LIMITATIONS ────────────────────────────────────────┐
│  Lua scripts can only access keys in a SINGLE slot
│
│  Same-slot Lua: SUCCESS
│  Cross-slot Lua: ERROR - CROSSSLOT Keys don't hash to same slot
│
│  Solution: Use hash tags to co-locate related keys!
└───────────────────────────────────────────────────────────────────┘
```

#### Step 2: Design Your Own Key Pattern

**Exercise:** Design key patterns for a shopping cart system:
- User profile
- User's cart items
- User's order history
- User's wishlist

**Your Answer (use hash tags):**
```
{user:123}:profile
{user:123}:cart
{user:123}:orders
{user:123}:wishlist
```

#### Step 3: Verify Your Design

```bash
# Test your pattern
docker exec redis-1 redis-cli -c -p 7001 \
    CLUSTER KEYSLOT "{user:123}:profile"
docker exec redis-1 redis-cli -c -p 7001 \
    CLUSTER KEYSLOT "{user:123}:cart"
```

**Expected:** Both should return the SAME slot number.

✅ **Checkpoint:** All keys for the same user should hash to the same slot.

---

### 📝 Lab 2 Summary

**What You Did:**
- ✅ Calculated hash slots for various keys
- ✅ Understood cross-slot limitations
- ✅ Applied hash tags for key co-location
- ✅ Designed atomic operation-friendly key patterns

**Key Takeaways:**
- CRC16(key) % 16384 determines slot
- Hash tags `{tag}` ensure keys share the same slot
- Cross-slot operations (MGET, MULTI, Lua) require hash tags
- Pattern: `{entity:id}:attribute`

---

## Lab 3: Scaling & Replication (40 minutes)

### 🎓 Learning Objectives
- Add a new master node (scale horizontally)
- Understand slot rebalancing
- Add replica for high availability
- Verify replication status

---

### Exercise 3.1: Add a Master Node (20 minutes)

#### Step 1: Check Current State

```bash
make slot-info
```

**Expected Output (BEFORE scaling):**
```
║  172.30.0.11:7001  5461   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.12:7002  5462   33.3%   ██████████░░░░░░░░░░           ║
║  172.30.0.13:7003  5461   33.3%   ██████████░░░░░░░░░░           ║
```

**Observation:** 3 masters, each with ~33% of slots.

#### Step 2: Create Test Data Before Scaling

```bash
# Create some test data to verify it survives migration
docker exec redis-1 redis-cli -c -p 7001 SET {scaling:test}:before "data-before-scaling"
docker exec redis-1 redis-cli -c -p 7001 SET {scaling:test}:timestamp "$(date)"
```

#### Step 3: Add New Master Node

```bash
make scale-up
```

**Expected Output:**
```
Starting new node redis-7...
[+] Running 1/1
 ✔ Container redis-7  Started
Adding node to cluster...
>>> Adding node 172.30.0.17:7007 to cluster 172.30.0.11:7001
>>> Performing Cluster Check
[OK] All nodes agree about slots configuration.
>>> Send CLUSTER MEET to node 172.30.0.17:7007
[OK] New node added correctly.

Rebalancing cluster...
>>> Performing Cluster Check
>>> Rebalancing across 4 nodes...
Moving 1365 slots from 172.30.0.11:7001 to 172.30.0.17:7007
Moving 1366 slots from 172.30.0.12:7002 to 172.30.0.17:7007
Moving 1365 slots from 172.30.0.13:7003 to 172.30.0.17:7007
[OK] All 16384 slots covered.
```

#### Step 4: Verify New Slot Distribution

```bash
make slot-info
```

**Expected Output (AFTER scaling):**
```
║  172.30.0.11:7001  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.12:7002  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.13:7003  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
║  172.30.0.17:7007  4096   25.0%   █████░░░░░░░░░░░░░░░           ║
```

**Observation:** Now 4 masters, each with exactly 25% of slots (4,096 slots).

#### Step 5: Verify Data Survived Migration

```bash
docker exec redis-1 redis-cli -c -p 7001 GET {scaling:test}:before
docker exec redis-1 redis-cli -c -p 7001 GET {scaling:test}:timestamp
```

**Expected Output:**
```
"data-before-scaling"
"<your timestamp>"
```

#### Step 6: Check Cluster Nodes

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep master
```

**Expected Output:**
```
<id-1> 172.30.0.11:7001@17001 master - 0 ... connected 0-4095
<id-2> 172.30.0.12:7002@17002 master - 0 ... connected 4096-8191
<id-3> 172.30.0.13:7003@17003 master - 0 ... connected 8192-12287
<id-7> 172.30.0.17:7007@17007 master - 0 ... connected 12288-16383
```

✅ **Checkpoint:**
- 4 masters visible
- Each master has ~4,096 slots
- Data still accessible
- cluster_state:ok

---

### Exercise 3.2: Add a Replica Node (20 minutes)

#### Step 1: Check Replica Coverage

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep -E "master|slave"
```

**Expected Output:**
```
<id> 172.30.0.11:7001 master - ... 0-4095
<id> 172.30.0.12:7002 master - ... 4096-8191
<id> 172.30.0.13:7003 master - ... 8192-12287
<id> 172.30.0.17:7007 master - ... 12288-16383  ← NO REPLICA!
<id> 172.30.0.14:7004 slave <master-1-id> ...
<id> 172.30.0.15:7005 slave <master-2-id> ...
<id> 172.30.0.16:7006 slave <master-3-id> ...
```

**Problem:** Master 4 (redis-7) has no replica - vulnerable to data loss!

#### Step 2: Add Replica for Master 4

```bash
make scale-add-replica
```

**Expected Output:**
```
Starting new node redis-8...
[+] Running 1/1
 ✔ Container redis-8  Started
Finding master node ID for redis-7...
Adding redis-8 as replica of <master-7-id>...
>>> Adding node 172.30.0.18:7008 to cluster 172.30.0.11:7001
>>> Performing Cluster Check
>>> Configure node as replica of 172.30.0.17:7007
[OK] New node added correctly.
```

#### Step 3: Verify Replication Started

```bash
docker exec redis-8 redis-cli -p 7008 INFO replication
```

**Expected Output:**
```
# Replication
role:slave
master_host:172.30.0.17
master_port:7007
master_link_status:up
master_last_io_seconds_ago:0
master_sync_in_progress:0
slave_repl_offset:123456
slave_priority:100
slave_read_only:1
```

**Key Fields to Check:**
- `role:slave` ✓
- `master_link_status:up` ✓
- `master_sync_in_progress:0` (0 = sync complete) ✓

#### Step 4: Check from Master's Perspective

```bash
docker exec redis-7 redis-cli -p 7007 INFO replication
```

**Expected Output:**
```
# Replication
role:master
connected_slaves:1
slave0:ip=172.30.0.18,port=7008,state=online,offset=123456,lag=0
master_failover_state:no-failover
master_replid:...
```

**Key Fields:**
- `connected_slaves:1` ✓
- `state=online` ✓
- `lag=0` (or very small number) ✓

#### Step 5: Test Replication

```bash
# Write to master
docker exec redis-7 redis-cli -p 7007 SET {test:repl}:data "replication-test"

# Wait 1 second for replication
sleep 1

# Read from replica (direct connection without -c flag)
docker exec redis-8 redis-cli -p 7008 GET {test:repl}:data
```

**Expected Output:**
```
-> Redirected to slot [12345] located at 172.30.0.17:7007
"replication-test"
```

#### Step 6: Verify Complete Cluster

```bash
make cluster-info
```

**Expected Output:**
```
cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:8  ← Now 8 nodes!
cluster_size:4         ← 4 masters
```

✅ **Checkpoint:**
- 8 total nodes (4 masters + 4 replicas)
- All masters have replicas
- Replication lag is low (0-2 seconds)
- cluster_state:ok

---

### 📝 Lab 3 Summary

**What You Did:**
- ✅ Scaled from 3 to 4 master nodes
- ✅ Observed zero-downtime slot rebalancing
- ✅ Added replica for new master
- ✅ Verified replication status

**Key Takeaways:**
- Horizontal scaling redistributes slots automatically
- Slot migration happens with zero downtime
- Every master needs at least one replica
- Replication is asynchronous (small lag expected)

---

## Lab 4: Automatic Failover (30 minutes)

### 🎓 Learning Objectives
- Simulate master node failure
- Observe automatic failover process
- Verify data integrity after failover
- Recover failed node

---

### Exercise 4.1: Setup Monitoring (5 minutes)

#### Step 1: Open Two Terminal Windows

**Terminal 1:** Start cluster monitor
```bash
make watch-cluster
```

**Expected Output (updates every 2 seconds):**
```
=== Redis Cluster Status (updated every 2s) ===

<id> 172.30.0.11:7001 master - 0 ... connected 0-4095
<id> 172.30.0.12:7002 master - 0 ... connected 4096-8191
...

--- Cluster Info ---
cluster_state:ok
cluster_slots_ok:16384
```

**Terminal 2:** Use for commands (keep Terminal 1 visible)

✅ **Checkpoint:** Terminal 1 shows live cluster status updating every 2 seconds.

---

### Exercise 4.2: Create Test Data (5 minutes)

#### Step 1: Create Test Data on Master 1

**In Terminal 2:**
```bash
# Create test data with hash tags (will be on Master 1's slots)
docker exec redis-1 redis-cli -c -p 7001 SET {failover:test}:critical "important-data"
docker exec redis-1 redis-cli -c -p 7001 SET {failover:test}:timestamp "$(date)"
docker exec redis-1 redis-cli -c -p 7001 SET {failover:test}:counter "42"
```

#### Step 2: Verify Which Node Stores It

```bash
make key-slot KEY="{failover:test}:critical"
```

**Expected Output:**
```
Key: {failover:test}:critical
Slot: 4768
Node: 172.30.0.11:7001  ← Master 1
```

#### Step 3: Verify Data is Readable

```bash
docker exec redis-1 redis-cli -c -p 7001 MGET \
    {failover:test}:critical \
    {failover:test}:timestamp \
    {failover:test}:counter
```

**Expected Output:**
```
1) "important-data"
2) "<your timestamp>"
3) "42"
```

✅ **Checkpoint:** Test data created and accessible on Master 1.

---

### Exercise 4.3: Trigger Failover (10 minutes)

#### Step 1: Note Current Cluster State

**In Terminal 1 (watch-cluster output):**
- Look for Master 1: `172.30.0.11:7001 master`
- Look for Replica 4: `172.30.0.14:7004 slave`

#### Step 2: Stop Master 1

**In Terminal 2:**
```bash
docker stop redis-1
```

**Expected Output:**
```
redis-1
```

#### Step 3: Watch Terminal 1 - Observe Failover

**You'll see changes happen in stages:**

**Stage 1 (2-4 seconds): PFAIL**
```
<id> 172.30.0.11:7001 master,pfail - 0 ... connected 0-4095
```
**Meaning:** Nodes suspect Master 1 has failed.

**Stage 2 (5 seconds): FAIL**
```
<id> 172.30.0.11:7001 master,fail - 0 ... connected 0-4095
```
**Meaning:** Majority confirmed failure.

**Stage 3 (6-8 seconds): PROMOTION**
```
<id> 172.30.0.14:7004 master - 0 ... connected 0-4095
```
**Meaning:** Replica 4 promoted to master! Takes over slots 0-4095.

**Stage 4 (10+ seconds): STABLE**
```
cluster_state:ok
cluster_known_nodes:7  ← One less node
```

#### Step 4: Note Failover Timing

**Record the timeline:**
- Time when docker stop executed: _______
- Time when PFAIL appeared: _______
- Time when FAIL appeared: _______
- Time when promotion complete: _______
- **Total failover time:** _______ seconds (typically 6-10 seconds)

✅ **Checkpoint:** Replica 4 should now be a master, and cluster_state should be "ok".

---

### Exercise 4.4: Verify Data Integrity (5 minutes)

#### Step 1: Access Data Through Cluster

**In Terminal 2:**
```bash
# Connect to any node - should auto-redirect
docker exec redis-2 redis-cli -c -p 7002 GET {failover:test}:critical
```

**Expected Output:**
```
-> Redirected to slot [4768] located at 172.30.0.14:7004
"important-data"
```

**Observation:** Data redirected to NEW master (redis-4)!

#### Step 2: Retrieve All Test Data

```bash
docker exec redis-2 redis-cli -c -p 7002 MGET \
    {failover:test}:critical \
    {failover:test}:timestamp \
    {failover:test}:counter
```

**Expected Output:**
```
1) "important-data"
2) "<your timestamp>"
3) "42"
```

#### Step 3: Verify New Master

```bash
docker exec redis-4 redis-cli -p 7004 ROLE
```

**Expected Output:**
```
1) "master"
2) (integer) 1234567890
3) (empty array)  ← No replicas yet
```

**Observation:** redis-4 is now a master with NO replica (vulnerable!).

✅ **Checkpoint:** All data is intact and accessible after failover. Zero data loss!

---

### Exercise 4.5: Recover Failed Node (5 minutes)

#### Step 1: Restart Failed Node

**In Terminal 2:**
```bash
docker start redis-1
```

**Expected Output:**
```
redis-1
```

#### Step 2: Watch Terminal 1 - Observe Rejoin

**You'll see:**
```
<id> 172.30.0.11:7001 slave <new-master-id> - 0 ... connected
```

**Observation:** redis-1 rejoined as REPLICA of redis-4 (the new master)!

#### Step 3: Verify New Role

```bash
docker exec redis-1 redis-cli -p 7001 ROLE
```

**Expected Output:**
```
1) "slave"
2) "172.30.0.14"
3) (integer) 7004
4) "connected"
5) (integer) 1234567890
```

#### Step 4: Check Cluster Status

```bash
make cluster-info
```

**Expected Output:**
```
cluster_state:ok
cluster_known_nodes:8  ← Back to 8 nodes
cluster_size:4
```

#### Step 5: Verify Data on Recovered Node

```bash
# Give it a moment to sync
sleep 2

# Read from recovered node
docker exec redis-1 redis-cli -p 7001 GET {failover:test}:critical
```

**Expected Output:**
```
-> Redirected to slot [4768] located at 172.30.0.14:7004
"important-data"
```

✅ **Checkpoint:**
- redis-1 rejoined as replica
- Data synchronized
- cluster_state:ok
- Full redundancy restored

---

### 📝 Lab 4 Summary

**What You Did:**
- ✅ Simulated master failure
- ✅ Observed automatic failover (~6-10 seconds)
- ✅ Verified zero data loss
- ✅ Recovered failed node (auto-rejoined as replica)

**Key Takeaways:**
- Failover is fully automatic (no manual intervention)
- Replica promotion happens in ~6-10 seconds
- Data integrity maintained (if replication caught up)
- Failed nodes auto-rejoin as replicas

**Failover Timeline:**
1. Master stops (0s)
2. PFAIL - Individual suspicion (2-4s)
3. FAIL - Majority confirms (5s)
4. Election - Replica wins vote (5-6s)
5. Promotion - Replica becomes master (6s)
6. Operational - Cluster healthy (6-10s)

---

## 🎓 Workshop Completion Checklist

### Lab 1: Cluster Setup ✅
- [ ] Started 6-node cluster
- [ ] Verified cluster health
- [ ] Understood slot distribution
- [ ] Performed basic operations

### Lab 2: Hash Slots ✅
- [ ] Calculated hash slots
- [ ] Experienced CROSSSLOT errors
- [ ] Applied hash tags successfully
- [ ] Designed key patterns

### Lab 3: Scaling ✅
- [ ] Added master node (4 masters total)
- [ ] Observed slot rebalancing
- [ ] Added replica for HA
- [ ] Verified replication

### Lab 4: Failover ✅
- [ ] Simulated master failure
- [ ] Observed automatic promotion
- [ ] Verified data integrity
- [ ] Recovered failed node

---

## 🔧 Troubleshooting Guide

### Issue 1: Cluster Not Starting

**Symptom:** `make start` fails or times out

**Solutions:**
```bash
# Clean everything and retry
make clean
docker system prune -f
make start

# Check Docker logs
docker logs redis-1

# Verify ports are not in use
netstat -an | grep 700[1-6]
```

---

### Issue 2: CROSSSLOT Errors

**Symptom:** `(error) CROSSSLOT Keys in request don't hash to the same slot`

**Solution:**
```bash
# Use hash tags to co-locate keys
# BAD:  MSET user:1 "a" user:2 "b"
# GOOD: MSET {user:1}:name "a" {user:1}:email "b"
```

---

### Issue 3: Replication Not Working

**Symptom:** `master_link_status:down`

**Solutions:**
```bash
# Check if master is running
docker ps | grep redis-7

# Check replication info
docker exec redis-8 redis-cli -p 7008 INFO replication

# Check network connectivity
docker exec redis-8 ping 172.30.0.17

# Manually set replica (if needed)
docker exec redis-8 redis-cli -p 7008 CLUSTER REPLICATE <master-node-id>
```

---

### Issue 4: Cluster State FAIL

**Symptom:** `cluster_state:fail`

**Solutions:**
```bash
# Check slot coverage
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS

# Check for failed nodes
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep fail

# Fix cluster (if needed)
docker exec redis-1 redis-cli -p 7001 --cluster fix 172.30.0.11:7001
```

---

## 📚 Quick Reference

### Essential Commands

```bash
# Cluster Management
make start                    # Start cluster
make stop                     # Stop cluster
make clean                    # Remove all data
make cluster-info             # View cluster status
make cluster-nodes            # View all nodes

# Monitoring
make watch-cluster            # Live monitoring
make slot-info                # Slot distribution

# Scaling
make scale-up                 # Add master node
make scale-add-replica        # Add replica node

# Testing
make failover                 # Simulate failure
make recover                  # Restart failed node
make key-slot KEY="mykey"     # Find key's slot

# Demos
make hash-tag-demo            # Hash tag demo
make cross-slot-demo          # Cross-slot demo
```

### Direct Redis CLI Commands

```bash
# Cluster info
docker exec redis-1 redis-cli -p 7001 CLUSTER INFO
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS

# Key operations
docker exec redis-1 redis-cli -c -p 7001 SET key value
docker exec redis-1 redis-cli -c -p 7001 GET key
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT key

# Replication
docker exec redis-4 redis-cli -p 7004 ROLE
docker exec redis-4 redis-cli -p 7004 INFO replication
```

---

## 🎯 Next Steps

### Continue Learning
1. Practice Labs 1-4 until comfortable
2. Experiment with different key patterns
3. Test multiple failover scenarios
4. Benchmark cluster performance

### Advanced Topics
- Multi-datacenter replication
- Redis modules (JSON, Search)
- Backup and restore strategies
- Monitoring with Prometheus

### Production Preparation
- Review security settings (passwords, TLS)
- Plan capacity (memory, CPU)
- Design backup strategy
- Document runbooks

---

## 🧹 Cleanup

When finished with the workshop:

```bash
# Stop all containers
make stop

# Remove containers and data
make clean

# Verify cleanup
docker ps -a | grep redis
```

---

## 📝 Feedback Form

**Workshop Date:** __________

**What worked well:**
- [ ] Lab instructions were clear
- [ ] Timing was appropriate
- [ ] Commands worked as expected
- [ ] Learning objectives met

**What needs improvement:**
- ____________________________________
- ____________________________________

**Most valuable exercise:** __________

**Most challenging part:** __________

**Overall Rating:** ☐ 1 ☐ 2 ☐ 3 ☐ 4 ☐ 5

---

**🎉 Congratulations! You've completed the Redis Cluster Workshop!**

You now have hands-on experience with:
- Redis Cluster architecture and setup
- Hash slots and key distribution
- Horizontal scaling operations
- High availability and automatic failover

**Keep practicing and exploring Redis Cluster!**
