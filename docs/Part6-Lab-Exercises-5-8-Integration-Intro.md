# Part 6: Lab Exercises (Labs 5-8) & PostgreSQL Integration Introduction

## Overview

This part covers advanced lab exercises including replicas, failover testing, scaling down, and load testing. It also introduces the concepts of integrating Redis Cluster with PostgreSQL.

---

## Lab 5: Adding Replica Nodes

### Objective
Improve redundancy by adding a replica to the newly added master.

### Step 1: Start Replica Container

```bash
docker compose up -d redis-8
```

### Step 2: Add as Replica

```bash
# Add redis-8 as replica of redis-7
make scale-add-replica
```

**Behind the scenes:**
```bash
redis-cli --cluster add-node \
    172.30.0.18:7008 \
    172.30.0.17:7007 \
    --cluster-slave
```

### Step 3: Verify Replication

```bash
make cluster-nodes
```

Expected output shows redis-8 as replica:
```
<node-id> 172.30.0.18:7008 slave <master-7-id> 0 1705312345 8 connected
```

### Step 4: Check Replication Status

```bash
# Connect to replica
redis-cli -p 7008 INFO replication
```

**Key metrics:**
```
role:slave
master_host:172.30.0.17
master_port:7007
master_link_status:up
slave_repl_offset:12345
```

### Key Learning Points

1. **Replicas sync automatically** from their master
2. **Replication lag** should be minimal (check offset)
3. **Each master should have 1+ replicas** for HA
4. **Replicas can serve read traffic** (RouteRandomly option)

---

## Lab 6: Failover Testing

### Objective
Understand automatic failover behavior when a master fails.

### Step 1: Identify Current Master-Replica Pairs

```bash
make cluster-nodes | grep -E "(master|slave)"
```

### Step 2: Record Current State

Before failover:
```
Master 1 (7001) → Replica 4 (7004)
Master 2 (7002) → Replica 5 (7005)
Master 3 (7003) → Replica 6 (7006)
```

### Step 3: Create Test Data

```bash
# Create event on Master 1's slots
./app create-event --name "Failover Test" --seats 10
```

Note the event ID and verify it's on Master 1 (check slot range 0-5460).

### Step 4: Simulate Master Failure

```bash
# Stop Master 1
make failover
```

**Behind the scenes:**
```bash
docker stop redis-1
```

### Step 5: Watch Failover Process

```bash
# In another terminal, watch cluster state
make watch-cluster
```

**Timeline you'll observe:**
```
0s   - Master 1 stops responding
1-2s - Replica 4 detects failure
3-5s - Cluster marks Master 1 as PFAIL
5s   - Consensus reached: FAIL status
6s   - Replica 4 promoted to Master
```

### Step 6: Verify Promotion

```bash
make cluster-nodes
```

New state:
```
<node-id> 172.30.0.14:7004 master - 0 ... 4 connected 0-5460
<node-id> 172.30.0.11:7001 master,fail ...
```

Replica 4 is now Master 4 with slots 0-5460!

### Step 7: Verify Data Integrity

```bash
# Access the event (should work via new master)
./app availability --event <event-id>
```

Data is preserved because replica had synced copy.

### Step 8: Recover Old Master

```bash
make recover
```

```bash
docker start redis-1
```

Old Master 1 rejoins as a replica of new Master 4.

### Key Learning Points

1. **Automatic detection** via gossip protocol (~5 seconds)
2. **Automatic promotion** of healthy replica
3. **Zero data loss** with properly synced replicas
4. **Client transparency** - MOVED redirections handle routing

---

## Lab 7: Scaling Down

### Objective
Learn safe node removal with slot migration.

### Step 1: Identify Target Node

We'll remove redis-7 (the master we added in Lab 4).

```bash
# Get node ID for redis-7
redis-cli -p 7007 CLUSTER MYID
```

### Step 2: Check Current Slot Assignment

```bash
redis-cli -c -p 7007 CLUSTER SLOTS
```

Note how many slots redis-7 owns (~4096).

### Step 3: Reshard Slots Away

```bash
make scale-down NODE=7007
```

**Behind the scenes:**
```bash
# Move slots from redis-7 to other masters
redis-cli --cluster reshard 127.0.0.1:7001 \
    --cluster-from <redis-7-id> \
    --cluster-to <redis-1-id> \
    --cluster-slots 2000 \
    --cluster-yes

# Repeat for other masters until redis-7 is empty
```

### Step 4: Verify Node is Empty

```bash
redis-cli -c -p 7001 CLUSTER SLOTS | grep 7007
```

Should return nothing (no slots assigned).

### Step 5: Remove from Cluster

```bash
redis-cli --cluster del-node \
    127.0.0.1:7001 \
    <redis-7-node-id>
```

### Step 6: Stop Container

```bash
docker stop redis-7
```

### Step 7: Verify Cluster Health

```bash
make cluster-info
```

Cluster should show:
- 3 masters
- cluster_state:ok
- All 16384 slots assigned

### Key Learning Points

1. **Reshard before remove** - never remove a node with data
2. **Gradual migration** - slots move without downtime
3. **Remove replica first** if the master has one
4. **Verify empty** before del-node command

---

## Lab 8: Load Testing

### Objective
Test cluster behavior under concurrent load.

### Step 1: Create Test Event

```bash
./app create-event --name "Load Test Event" --seats 100
```

### Step 2: Run Load Test

```bash
make load-test USERS=50 SEATS=2
```

This spawns 50 concurrent users, each trying to reserve 2 seats.

### Step 3: Observe Results

**Expected output:**
```
Load Test Results:
- Total requests: 50
- Successful reservations: 50
- Failed (seat taken): 0
- Average latency: 12ms
- P99 latency: 45ms
```

With 100 seats and 50 users wanting 2 seats each, all should succeed.

### Step 4: Test Contention

```bash
# More users than seats available
make load-test USERS=100 SEATS=2
```

Now 100 users want 200 seats but only 100 exist:
```
Load Test Results:
- Total requests: 100
- Successful reservations: 50
- Failed (seat taken): 50
- Average latency: 15ms
```

### Step 5: Monitor During Load

```bash
# In another terminal
watch -n 1 'redis-cli -p 7001 INFO stats | grep instantaneous'
```

Key metrics:
- `instantaneous_ops_per_sec` - operations per second
- `instantaneous_input_kbps` - network input
- `instantaneous_output_kbps` - network output

### Key Learning Points

1. **Lua scripts prevent race conditions** even under high load
2. **Latency remains low** due to in-memory operations
3. **Cluster distributes load** across masters
4. **Monitor throughput and latency** for capacity planning

---

## Introduction to PostgreSQL Integration

### Why Integrate Redis with PostgreSQL?

Redis Cluster excels at:
- Real-time data (millisecond latency)
- Session management
- Caching
- Rate limiting
- Distributed locks

PostgreSQL excels at:
- Durable storage (survives restarts)
- Complex queries (JOIN, aggregation)
- Transactions (ACID compliance)
- Historical data
- Reporting

### The Hybrid Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    HYBRID ARCHITECTURE                           │
│                                                                  │
│                      ┌─────────────┐                            │
│                      │  Application │                            │
│                      └──────┬──────┘                            │
│                             │                                    │
│              ┌──────────────┼──────────────┐                    │
│              │              │              │                    │
│              ▼              ▼              ▼                    │
│     ┌─────────────┐  ┌───────────┐  ┌───────────────┐          │
│     │ Redis       │  │ PostgreSQL │  │ Message Queue │          │
│     │ Cluster     │  │            │  │ (Optional)    │          │
│     │             │  │            │  │               │          │
│     │ • Hot data  │  │ • Source   │  │ • Async sync  │          │
│     │ • Sessions  │  │   of truth │  │ • Events      │          │
│     │ • Counters  │  │ • History  │  │               │          │
│     │ • Real-time │  │ • Reports  │  │               │          │
│     └─────────────┘  └───────────┘  └───────────────┘          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Data Responsibility Split

| Data Type | Redis Cluster | PostgreSQL |
|-----------|--------------|------------|
| Real-time seat availability | Primary | Sync periodically |
| Pending reservations (with TTL) | Primary | - |
| Confirmed reservations | Cache | Primary |
| User sessions | Primary | - |
| Event metadata | Cache | Primary |
| Transaction history | - | Primary |
| Analytics/Reports | - | Primary |

### Key Integration Patterns

1. **Write-Through**: Write to both simultaneously
2. **Cache-Aside**: Read from cache, fall back to database
3. **Event-Driven**: Async sync via message queue
4. **Periodic Reconciliation**: Scheduled sync jobs

---

## Summary: Labs 5-8

| Lab | Skill | Key Takeaway |
|-----|-------|--------------|
| 5 | Adding replicas | Every master needs redundancy |
| 6 | Failover testing | ~6 second automatic recovery |
| 7 | Scaling down | Reshard before remove |
| 8 | Load testing | Lua prevents race conditions |

### Next Steps

Part 7 covers detailed PostgreSQL integration patterns:
- Write-through caching
- Event-driven synchronization
- Schema design
- Complete reservation flow with both systems
