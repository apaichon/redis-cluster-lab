# Lab 1: Redis Cluster Setup & Basics - Detailed Guide

## Overview

This lab provides hands-on experience with initializing a Redis Cluster, understanding its topology, and verifying its basic configuration. By the end of this lab, you will understand how Redis Cluster distributes data across multiple nodes and how master-replica relationships provide high availability.

---

## Prerequisites

Before starting this lab, ensure you have:

| Requirement | Version | Verification Command |
|-------------|---------|---------------------|
| Docker | 20.10+ | `docker --version` |
| Docker Compose | 2.0+ | `docker compose version` |
| Go | 1.21+ | `go version` |
| Make | Any | `make --version` |
| Terminal | bash/zsh | - |

---

## Lab Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     REDIS CLUSTER TOPOLOGY                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐         │
│  │   MASTER 1      │  │   MASTER 2      │  │   MASTER 3      │         │
│  │   redis-1       │  │   redis-2       │  │   redis-3       │         │
│  │   Port: 7001    │  │   Port: 7002    │  │   Port: 7003    │         │
│  │   IP: 172.30.0.11│  │   IP: 172.30.0.12│  │   IP: 172.30.0.13│        │
│  │                 │  │                 │  │                 │         │
│  │  Slots: 0-5460  │  │ Slots: 5461-10922│ │ Slots: 10923-16383│        │
│  │  (5461 slots)   │  │  (5462 slots)   │  │  (5461 slots)   │         │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘         │
│           │                    │                    │                   │
│           │ replication        │ replication        │ replication       │
│           ▼                    ▼                    ▼                   │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐         │
│  │   REPLICA 1     │  │   REPLICA 2     │  │   REPLICA 3     │         │
│  │   redis-4       │  │   redis-5       │  │   redis-6       │         │
│  │   Port: 7004    │  │   Port: 7005    │  │   Port: 7006    │         │
│  │   IP: 172.30.0.14│  │   IP: 172.30.0.15│  │   IP: 172.30.0.16│        │
│  │                 │  │                 │  │                 │         │
│  │  (standby for   │  │  (standby for   │  │  (standby for   │         │
│  │   Master 1)     │  │   Master 2)     │  │   Master 3)     │         │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘         │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │                     DOCKER NETWORK                                │  │
│  │                   redis-cluster (172.30.0.0/24)                   │  │
│  └──────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │                     SPARE NODES (for scaling labs)                │  │
│  │   redis-7 (7007, 172.30.0.17)    redis-8 (7008, 172.30.0.18)     │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Hash Slot Distribution

Redis Cluster uses **16,384 hash slots** to distribute data across nodes:

```
Total Slots: 16,384 (0 to 16,383)

┌─────────────────────────────────────────────────────────────────────────┐
│                        HASH SLOT DISTRIBUTION                            │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Slot 0                    Slot 16,383                                  │
│  │                                │                                     │
│  ▼                                ▼                                     │
│  ├──────────────┼──────────────┼──────────────┤                        │
│  │  Master 1    │  Master 2    │  Master 3    │                        │
│  │  0 - 5460    │  5461-10922  │ 10923-16383  │                        │
│  │  (5461)      │  (5462)      │  (5461)      │                        │
│  ├──────────────┼──────────────┼──────────────┤                        │
│                                                                         │
│  Key "user:1001" → CRC16("user:1001") % 16384 = slot 12539             │
│                    → Stored on Master 3 (owns slots 10923-16383)        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Step-by-Step Exercise

### Step 1: Navigate to Project Directory

```bash
cd /path/to/cluster-labs
```

### Step 2: Start the Cluster

```bash
make start
```

#### What `make start` Does:

```makefile
start:
    @echo "Starting Redis cluster (6 nodes)..."
    docker compose up -d                    # 1. Start Docker containers
    @sleep 3                                # 2. Wait for containers to initialize
    @echo "Initializing cluster..."
    @chmod +x scripts/*.sh                  # 3. Make scripts executable
    @./scripts/init-cluster.sh              # 4. Initialize cluster topology
    @echo ""
    @echo "Cluster is ready! Run 'make cluster-info' to verify."
```

#### Detailed Breakdown:

**Phase 1: Docker Compose Up**
```bash
docker compose up -d
```

This command:
- Reads `docker-compose.yml` configuration
- Creates a custom Docker network (`redis-cluster` with subnet `172.30.0.0/24`)
- Pulls `redis:7.2-alpine` image if not present
- Creates 6 containers (redis-1 through redis-6) with:
  - Assigned static IP addresses (172.30.0.11 - 172.30.0.16)
  - Mapped ports (7001-7006 for Redis, 17001-17006 for cluster bus)
  - Mounted `redis.conf` configuration file
  - Persistent volumes for data storage
- Starts Redis server in cluster mode on each container

**Phase 2: Wait for Readiness**
```bash
sleep 3
```

Allows containers to fully initialize before cluster creation.

**Phase 3: Initialize Cluster Topology**
```bash
./scripts/init-cluster.sh
```

The init script performs:

```bash
# 1. Wait for all nodes to respond to PING
for i in 1 2 3 4 5 6; do
    port=$((7000 + i))
    until docker exec redis-$i redis-cli -p $port ping | grep -q PONG; do
        echo "Waiting for redis-$i (port $port)..."
        sleep 1
    done
done

# 2. Check if cluster already exists (idempotent)
CLUSTER_INFO=$(docker exec redis-1 redis-cli -p 7001 cluster info)
if echo "$CLUSTER_INFO" | grep -q "cluster_state:ok"; then
    echo "Cluster is already initialized!"
    exit 0
fi

# 3. Create cluster with specified topology
docker exec redis-1 redis-cli --cluster create \
    172.30.0.11:7001 \    # Master 1
    172.30.0.12:7002 \    # Master 2
    172.30.0.13:7003 \    # Master 3
    172.30.0.14:7004 \    # Replica 1 → Master 1
    172.30.0.15:7005 \    # Replica 2 → Master 2
    172.30.0.16:7006 \    # Replica 3 → Master 3
    --cluster-replicas 1 \  # 1 replica per master
    --cluster-yes           # Auto-confirm
```

**The `--cluster create` command:**
- Distributes 16,384 slots across the first 3 nodes (masters)
- Assigns the remaining 3 nodes as replicas (1 per master)
- Establishes replication relationships
- Performs initial data synchronization

---

### Step 3: Verify Cluster State

```bash
make cluster-info
```

#### What `make cluster-info` Does:

```makefile
cluster-info:
    @echo ""
    @echo "Cluster Nodes:"
    @docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null || \
     docker exec redis-2 redis-cli -p 7002 cluster nodes
    @echo ""
    @echo "Cluster Info:"
    @docker exec redis-1 redis-cli -p 7001 cluster info 2>/dev/null | \
     grep -E "cluster_state|cluster_slots|cluster_known_nodes|cluster_size" || \
     docker exec redis-2 redis-cli -p 7002 cluster info | \
     grep -E "cluster_state|cluster_slots|cluster_known_nodes|cluster_size"
```

#### Understanding the Output:

**CLUSTER NODES Output:**
```
<node-id>  172.30.0.11:7001@17001 myself,master - 0 0 1 connected 0-5460
<node-id>  172.30.0.12:7002@17002 master - 0 1705312345 2 connected 5461-10922
<node-id>  172.30.0.13:7003@17003 master - 0 1705312346 3 connected 10923-16383
<node-id>  172.30.0.14:7004@17004 slave <master-1-id> 0 1705312347 4 connected
<node-id>  172.30.0.15:7005@17005 slave <master-2-id> 0 1705312348 5 connected
<node-id>  172.30.0.16:7006@17006 slave <master-3-id> 0 1705312349 6 connected
```

**Field Breakdown:**

| Field | Description | Example |
|-------|-------------|---------|
| `node-id` | 40-character unique identifier | `a1b2c3d4e5f6...` |
| `ip:port@bus-port` | Network address | `172.30.0.11:7001@17001` |
| `flags` | Node status flags | `myself,master` or `slave` |
| `master-id` | For replicas, the master's ID | `a1b2c3d4e5f6...` or `-` |
| `ping-sent` | Last ping timestamp | `0` |
| `pong-recv` | Last pong timestamp | `1705312345` |
| `config-epoch` | Configuration version | `1` |
| `link-state` | Connection status | `connected` |
| `slot-range` | Assigned hash slots (masters only) | `0-5460` |

**CLUSTER INFO Output:**
```
cluster_state:ok                    # Cluster is healthy
cluster_slots_assigned:16384        # All slots assigned
cluster_slots_ok:16384              # All slots functioning
cluster_slots_pfail:0               # No potentially failing slots
cluster_slots_fail:0                # No failed slots
cluster_known_nodes:6               # Total nodes in cluster
cluster_size:3                      # Number of masters
```

**Key Metrics to Verify:**

| Metric | Expected Value | Meaning |
|--------|----------------|---------|
| `cluster_state` | `ok` | Cluster is operational |
| `cluster_slots_assigned` | `16384` | All slots are assigned to nodes |
| `cluster_slots_ok` | `16384` | All slots are functioning |
| `cluster_known_nodes` | `6` | All 6 nodes are recognized |
| `cluster_size` | `3` | 3 master nodes (shards) |

---

### Step 4: Check Node Roles

```bash
make cluster-nodes
```

This is an alias that runs the same command as `cluster-info` but focuses on node details.

#### Alternative Direct Commands:

```bash
# Connect to any node and view cluster state
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES

# View from localhost (requires port mapping)
redis-cli -c -p 7001 CLUSTER NODES

# Check specific node's role
docker exec redis-1 redis-cli -p 7001 ROLE
```

**ROLE Command Output for Master:**
```
1) "master"
2) (integer) 1234567890    # Replication offset
3) 1) 1) "172.30.0.14"     # Connected replica
      2) "7004"
      3) "1234567890"
```

**ROLE Command Output for Replica:**
```
1) "slave"
2) "172.30.0.11"           # Master IP
3) (integer) 7001          # Master port
4) "connected"             # Replication state
5) (integer) 1234567890    # Replication offset
```

---

## Additional Make Commands Reference

### Setup & Management Commands

| Command | Description | Behind the Scenes |
|---------|-------------|-------------------|
| `make build` | Compile Go application | `cd app && go build -o ticket-reservation .` |
| `make start` | Start cluster + initialize | `docker compose up -d` + init script |
| `make stop` | Stop all containers | `docker compose --profile scale down` |
| `make clean` | Remove containers + volumes | `docker compose --profile scale down -v` |
| `make init` | Re-initialize cluster | `./scripts/init-cluster.sh` |

### Cluster Operations Commands

| Command | Description | Behind the Scenes |
|---------|-------------|-------------------|
| `make cluster-info` | Show cluster status | `CLUSTER NODES` + `CLUSTER INFO` |
| `make scale-up` | Add new master (redis-7) | `./scripts/scale-add-master.sh` |
| `make scale-add-replica` | Add replica (redis-8) | `./scripts/scale-add-replica.sh` |
| `make scale-down NODE=7007` | Remove specific node | `./scripts/scale-remove-node.sh 7007` |
| `make failover` | Test automatic failover | `./scripts/failover-test.sh` |
| `make recover` | Recover failed node | `docker start redis-1` |
| `make watch-cluster` | Live cluster monitoring | Loop with `CLUSTER NODES` every 2s |

### Application Commands

| Command | Description | Behind the Scenes |
|---------|-------------|-------------------|
| `make demo` | Run full demonstration | `./ticket-reservation demo` |
| `make load-test USERS=50` | Concurrent load test | `./ticket-reservation load-test --users 50` |

### Sharding Lab Commands

| Command | Description | Example |
|---------|-------------|---------|
| `make slot-info` | Show slot distribution | Displays all 16,384 slot assignments |
| `make key-slot KEY=user:123` | Find slot for key | Shows which node stores the key |
| `make hash-tag-demo` | Demonstrate hash tags | Shows `{event:123}` co-location |
| `make cross-slot-demo` | Show cross-slot errors | Demonstrates CROSSSLOT limitation |
| `make sharding-demo` | Comprehensive demo | Full sharding walkthrough |

---

## Configuration Files

### redis.conf (Shared by All Nodes)

```conf
# Cluster mode enabled
cluster-enabled yes
cluster-config-file nodes.conf
cluster-node-timeout 5000          # 5 seconds to detect failure

# Persistence (AOF for durability)
appendonly yes
appendfilename "appendonly.aof"
appendfsync everysec

# Memory management
maxmemory 100mb
maxmemory-policy allkeys-lru       # Evict least recently used keys

# Networking
bind 0.0.0.0                       # Accept connections from any IP
protected-mode no                  # Allow connections without password
```

### docker-compose.yml Structure

```yaml
version: '3.8'

services:
  redis-1:
    image: redis:7.2-alpine
    container_name: redis-1
    command: >
      redis-server /usr/local/etc/redis/redis.conf
      --port 7001
      --cluster-announce-ip 172.30.0.11      # Advertised IP
      --cluster-announce-port 7001           # Client port
      --cluster-announce-bus-port 17001      # Cluster bus port
    ports:
      - "7001:7001"                           # Client connections
      - "17001:17001"                         # Cluster gossip
    volumes:
      - ./redis.conf:/usr/local/etc/redis/redis.conf
      - redis-1-data:/data
    networks:
      redis-cluster:
        ipv4_address: 172.30.0.11            # Static IP
    healthcheck:
      test: ["CMD", "redis-cli", "-p", "7001", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  # ... redis-2 through redis-6 follow same pattern ...

networks:
  redis-cluster:
    driver: bridge
    ipam:
      config:
        - subnet: 172.30.0.0/24

volumes:
  redis-1-data:
  # ... redis-2-data through redis-6-data ...
```

---

## Verification Checklist

After completing Lab 1, verify the following:

| Check | Command | Expected Result |
|-------|---------|-----------------|
| Cluster state | `make cluster-info` | `cluster_state:ok` |
| All slots assigned | `make cluster-info` | `cluster_slots_assigned:16384` |
| 6 nodes visible | `make cluster-info` | `cluster_known_nodes:6` |
| 3 masters | `make cluster-info` | `cluster_size:3` |
| Replicas connected | `CLUSTER NODES` | All replicas show `connected` |
| Slot distribution | `make slot-info` | ~5461 slots per master |

---

## Troubleshooting

### Cluster State is Not OK

```bash
# Check which slots are problematic
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS

# Check for failed nodes
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES | grep fail
```

### Connection Refused

```bash
# Verify containers are running
docker compose ps

# Check container logs
docker logs redis-1
```

### Nodes Not Joining Cluster

```bash
# Check if cluster mode is enabled
docker exec redis-1 redis-cli -p 7001 CONFIG GET cluster-enabled

# Manually trigger cluster meet
docker exec redis-1 redis-cli -p 7001 CLUSTER MEET 172.30.0.12 7002
```

### Reset Cluster (Start Over)

```bash
make clean   # Remove all containers and volumes
make start   # Fresh start
```

---

## Key Learning Points

1. **Cluster Initialization**: The `--cluster create` command automatically distributes slots and assigns replicas

2. **Hash Slots**: 16,384 total slots distributed across masters; determines where each key is stored

3. **Master-Replica Relationship**: Each master has one replica for high availability; replicas sync automatically

4. **Cluster State**: `cluster_state:ok` indicates a healthy, operational cluster

5. **Network Architecture**: Docker bridge network with static IPs ensures consistent node addressing

6. **Ports**: Each node uses two ports - client port (700x) and cluster bus port (1700x)

---

## Next Lab

**Lab 2: Hash Slots & Key Distribution** - Learn how keys map to specific nodes and practice using hash tags for data co-location.

```bash
# Preview Lab 2
make key-slot KEY=user:1001
make hash-tag-demo
```
