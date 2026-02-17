# Redis Cluster - Command Cheatsheet

---

## Make Commands (Lab Controls)

| Command | What it Does |
|---------|-------------|
| `make start` | Start 6-node cluster |
| `make stop` | Stop all containers |
| `make clean` | Stop + remove all data |
| `make cluster-info` | Show cluster state & nodes |
| `make slot-info` | Visual slot distribution |
| `make watch-cluster` | Live monitor (every 2s) |
| `make key-slot KEY="mykey"` | Show slot & node for a key |
| `make scale-up` | Add 4th master (redis-7) |
| `make scale-add-replica` | Add replica for Master 4 (redis-8) |
| `make failover` | Simulate master failure |
| `make recover` | Restart failed node |
| `make hash-tag-demo` | Hash tag co-location demo |
| `make cross-slot-demo` | Cross-slot error demo |

---

## Cluster State & Nodes

```bash
# Cluster summary
docker exec redis-1 redis-cli -p 7001 CLUSTER INFO

# All nodes (id, role, slots)
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES

# Filter masters only
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep master

# Filter slaves only
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep slave

# Slot ranges per node
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS
```

---

## Key & Slot Operations

```bash
# Find which slot a key maps to
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "mykey"

# List keys in a specific slot (up to 10)
docker exec redis-3 redis-cli -p 7003 CLUSTER GETKEYSINSLOT 14687 10

# SET a key (cluster mode - auto redirect)
docker exec redis-1 redis-cli -c -p 7001 SET mykey "Hello"

# GET a key (cluster mode)
docker exec redis-2 redis-cli -c -p 7002 GET mykey

# MSET with hash tags (same slot - works!)
docker exec redis-1 redis-cli -c -p 7001 MSET \
    "{user:1}:name" "Alice" \
    "{user:1}:email" "alice@test.com"

# MGET with hash tags
docker exec redis-1 redis-cli -c -p 7001 MGET \
    "{user:1}:name" \
    "{user:1}:email"

# MSET without hash tags (different slots - FAILS with CROSSSLOT)
docker exec redis-1 redis-cli -c -p 7001 MSET user:1 "Alice" user:2 "Bob"
```

---

## Replication & Role

```bash
# Check role of a node (master or slave)
docker exec redis-1 redis-cli -p 7001 ROLE
docker exec redis-4 redis-cli -p 7004 ROLE

# Replica replication status
docker exec redis-8 redis-cli -p 7008 INFO replication

# Master replication status (shows connected slaves)
docker exec redis-7 redis-cli -p 7007 INFO replication
```

**Key fields to check in `INFO replication`:**
- `role:slave` or `role:master`
- `master_link_status:up` (replica connected)
- `master_sync_in_progress:0` (initial sync done)
- `connected_slaves:1` (master sees replica)

---

## Failover Simulation

```bash
# Terminal 1 — watch live cluster changes
make watch-cluster

# Terminal 2 — create test data on Master 1
docker exec redis-1 redis-cli -c -p 7001 SET {failover:test}:data "critical"

# Kill Master 1
docker stop redis-1

# After failover — verify data via another node (auto-redirects)
docker exec redis-2 redis-cli -c -p 7002 GET {failover:test}:data

# Recover failed node (auto-rejoins as replica)
docker start redis-1

# Verify it rejoined as replica
docker exec redis-1 redis-cli -p 7001 ROLE
```

**Failover Timeline:** `0s` stop → `2-4s` PFAIL → `5s` FAIL → `6s` promotion → `6-10s` cluster ok

---

## Scaling

```bash
# Before scaling — check slot distribution
make slot-info

# Add 4th master (redis-7, port 7007) + auto rebalance
make scale-up

# After scaling — confirm 4x 25% each
make slot-info

# Check replica coverage (look for node with no slave)
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep -E "master|slave"

# Add replica for new master
make scale-add-replica

# Confirm 8 nodes in cluster
make cluster-info
```

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `CROSSSLOT` error | Use hash tags: `{tag}:key` |
| `cluster_state:fail` | `docker exec redis-1 redis-cli -p 7001 --cluster fix 172.30.0.11:7001` |
| `master_link_status:down` | Check master is running; verify network |
| Port conflicts on start | `make clean && make start` |
| Slow failover | Tune `cluster-node-timeout` (default 5000ms) |

```bash
# Full reset if things go wrong
make clean
docker system prune -f
make start

# Check Docker logs for a node
docker logs redis-1

# Check if ports are in use
netstat -an | grep 700[1-8]

# Find failed nodes
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep fail

# Manually trigger replicate (if needed)
docker exec redis-8 redis-cli -p 7008 CLUSTER REPLICATE <master-node-id>
```

---

## Container & Port Map

| Container | Port | Role (initial) |
|-----------|------|----------------|
| redis-1 | 7001 | Master 1 (slots 0-5460) |
| redis-2 | 7002 | Master 2 (slots 5461-10922) |
| redis-3 | 7003 | Master 3 (slots 10923-16383) |
| redis-4 | 7004 | Replica of Master 1 |
| redis-5 | 7005 | Replica of Master 2 |
| redis-6 | 7006 | Replica of Master 3 |
| redis-7 | 7007 | Master 4 (added in Lab 3) |
| redis-8 | 7008 | Replica of Master 4 (added in Lab 3) |

---

## Hash Tag Quick Reference

```
Pattern:  {entity:id}:attribute

Examples:
  {user:42}:profile       {user:42}:cart       {user:42}:orders
  {order:99}:items        {order:99}:status    {order:99}:payment
  {event:abc}:seats       {event:abc}:waitlist

Formula:  CRC16({tag}) % 16384 = slot
```

Keys sharing the same `{tag}` land on the **same slot** — enabling `MGET`, `MSET`, `MULTI/EXEC`, and Lua scripts across those keys.
