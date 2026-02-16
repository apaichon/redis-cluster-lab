# Lab 4: MOVED & ASK Redirections

## Overview

This lab teaches how Redis Cluster handles client redirections when keys are on different nodes or during slot migration. Understanding MOVED and ASK is essential for debugging cluster issues and building resilient applications.

---

## 1. Understanding Redirections

Redis Cluster uses two types of redirections to route clients to the correct node:

| Redirection | When It Occurs | Client Action |
|-------------|----------------|---------------|
| **MOVED** | Slot permanently lives on another node | Update slot cache, retry on new node |
| **ASK** | Slot is being migrated, key already moved | Send `ASKING` + retry (don't update cache) |

### Visual Comparison

```
MOVED (Permanent)                    ASK (Temporary)
─────────────────                    ────────────────
┌─────────┐                          ┌─────────┐
│ Client  │                          │ Client  │
└────┬────┘                          └────┬────┘
     │ GET foo                            │ GET bar
     ▼                                    ▼
┌─────────┐                          ┌─────────┐
│ Node A  │ ─── MOVED 12182 B ──▶    │ Node A  │ ─── ASK 0 B ──▶
└─────────┘                          └─────────┘
     │                                    │         (MIGRATING)
     │ Update cache:                      │
     │ slot 12182 → B                     │ Don't update cache
     │                                    │ (migration may rollback)
     ▼                                    ▼
┌─────────┐                          ┌─────────┐
│ Node B  │ ◀── All future           │ Node B  │ ◀── ASKING + GET bar
└─────────┘     requests             └─────────┘     (one-time)
```

---

## 2. Lab Exercises

### Prerequisites

Ensure your cluster is running:

```bash
make start
make cluster-info
```

---

### Exercise 1: Trigger MOVED Redirection

MOVED occurs when you access a key from the wrong node.

#### Step 1: Check Slot Distribution

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS
```

**Expected Output:**
```
1) 1) (integer) 0
   2) (integer) 5460
   3) 1) "172.30.0.11"
      2) (integer) 7001
      ...
2) 1) (integer) 5461
   2) (integer) 10922
   3) 1) "172.30.0.12"
      2) (integer) 7002
      ...
3) 1) (integer) 10923
   2) (integer) 16383
   3) 1) "172.30.0.13"
      2) (integer) 7003
```

This shows:
- Slots 0-5460 → Node 7001
- Slots 5461-10922 → Node 7002
- Slots 10923-16383 → Node 7003

#### Step 2: Find Which Slot a Key Maps To

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "foo"
```

**Output:** `12182`

Key "foo" maps to slot 12182, which is on Node 7003.

#### Step 3: Set the Key (Using Cluster Mode)

```bash
docker exec redis-1 redis-cli -p 7001 -c SET foo "bar"
```

The `-c` flag enables cluster mode, which auto-follows redirects.

#### Step 4: Trigger MOVED (Without Cluster Mode)

```bash
docker exec redis-1 redis-cli -p 7001 GET foo
```

**Output:**
```
(error) MOVED 12182 172.30.0.13:7003
```

**Explanation:**
- Node 7001 doesn't own slot 12182
- Redis returns `MOVED 12182 172.30.0.13:7003`
- This means: "Slot 12182 permanently lives on 172.30.0.13:7003"

#### Step 5: Verify with Cluster Mode

```bash
docker exec redis-1 redis-cli -p 7001 -c GET foo
```

**Output:** `"bar"`

With `-c` flag, the client automatically follows the redirect.

---

### Exercise 2: Trigger ASK Redirection

ASK occurs during live slot migration when a key has been moved but the slot hasn't been finalized.

#### Step 1: Create a Key in Slot 0

First, find a key that maps to slot 0:

```bash
# Check slot for key "{06S}test"
docker exec redis-1 redis-cli -p 7001 CLUSTER KEYSLOT "{06S}test"
```

**Output:** `0`

Create the key:

```bash
docker exec redis-1 redis-cli -p 7001 SET "{06S}test" "migration_test_value"
```

#### Step 2: Get Node IDs

```bash
# Source node (7001)
docker exec redis-1 redis-cli -p 7001 CLUSTER MYID
# Example: a4db5d781770d1464af237addf9d1dd90255e8d5

# Target node (7003)
docker exec redis-3 redis-cli -p 7003 CLUSTER MYID
# Example: 58760276d579aa72170a6bb3181b8a15a843adae
```

Save these IDs for the next steps.

#### Step 3: Start Slot Migration

Set slot 0 as MIGRATING on source node:

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER SETSLOT 0 MIGRATING <target-node-id>
```

Set slot 0 as IMPORTING on target node:

```bash
docker exec redis-3 redis-cli -p 7003 CLUSTER SETSLOT 0 IMPORTING <source-node-id>
```

**Expected Output:** `OK` for both commands.

#### Step 4: Migrate the Key

```bash
docker exec redis-1 redis-cli -p 7001 MIGRATE 172.30.0.13 7003 "{06S}test" 0 5000
```

**Output:** `OK`

The key is now on Node 7003, but slot ownership is still with 7001.

#### Step 5: Trigger ASK Redirection

```bash
docker exec redis-1 redis-cli -p 7001 GET "{06S}test"
```

**Output:**
```
(error) ASK 0 172.30.0.13:7003
```

**Explanation:**
- The key was migrated to 7003
- But slot 0 ownership hasn't been finalized
- Redis returns ASK (temporary redirect)

#### Step 6: Handle ASK Properly

To access the key, you must send `ASKING` first:

```bash
# Step 1: Send ASKING command
docker exec redis-3 redis-cli -p 7003 ASKING

# Step 2: Then get the key
docker exec redis-3 redis-cli -p 7003 GET "{06S}test"
```

**Output:** `"migration_test_value"`

#### Step 7: Clean Up - Cancel Migration

```bash
# Reset slot on both nodes
docker exec redis-1 redis-cli -p 7001 CLUSTER SETSLOT 0 NODE <source-node-id>
docker exec redis-3 redis-cli -p 7003 CLUSTER SETSLOT 0 NODE <source-node-id>

# Migrate key back
docker exec redis-3 redis-cli -p 7003 MIGRATE 172.30.0.11 7001 "{06S}test" 0 5000
```

---

## 3. Slot States During Migration

During resharding, slots go through specific states:

```
┌─────────────────────────────────────────────────────────────────────┐
│                     SLOT MIGRATION STATES                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   Source Node                      Target Node                       │
│   ┌─────────────────┐              ┌─────────────────┐               │
│   │ MIGRATING       │   ────────▶  │ IMPORTING       │               │
│   │ slot 100        │   keys move  │ slot 100        │               │
│   └─────────────────┘              └─────────────────┘               │
│                                                                      │
│   Behavior:                        Behavior:                         │
│   • Existing keys: serve locally   • Only accepts keys with ASKING   │
│   • Migrated keys: return ASK      • Rejects normal requests         │
│   • New keys: MOVED to target      • Until slot is finalized         │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### State Transitions

| State | Source Node | Target Node | Keys Location |
|-------|-------------|-------------|---------------|
| Before | Owns slot | - | All on source |
| MIGRATING/IMPORTING | MIGRATING | IMPORTING | Mixed |
| After SETSLOT NODE | - | Owns slot | All on target |

---

## 4. Client Library Handling

### How go-redis Handles Redirections

The go-redis cluster client automatically handles both redirections:

```go
// go-redis handles this internally:
// 1. Send command to any node
// 2. If MOVED: update slot mapping, retry
// 3. If ASK: send ASKING + command to target (no cache update)

client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},
})

// This works transparently even during resharding
val, err := client.Get(ctx, "mykey").Result()
```

### Manual Handling (if needed)

```go
import "github.com/redis/go-redis/v9"

func handleRedirection(err error) {
    if moved, ok := err.(*redis.MovedError); ok {
        // Permanent redirect
        fmt.Printf("Key moved to slot %d on %s\n", moved.Slot, moved.Addr)
        // Update your slot cache
        // Retry on new node
    }

    if ask, ok := err.(*redis.AskError); ok {
        // Temporary redirect (during migration)
        fmt.Printf("Key temporarily on %s (slot %d migrating)\n", ask.Addr, ask.Slot)
        // Send ASKING + retry on target
        // Do NOT update slot cache
    }
}
```

---

## 5. Debugging Commands

### Check Slot State

```bash
# View all slots and their states
docker exec redis-1 redis-cli -p 7001 CLUSTER SLOTS

# Check if slot is migrating
docker exec redis-1 redis-cli -p 7001 CLUSTER SHARDS
```

### Check Key Location

```bash
# Using the app
make get-key KEY_NAME="mykey"

# Using redis-cli
docker exec redis-1 redis-cli -p 7001 -c CLUSTER KEYSLOT "mykey"
```

### View Node State

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES
```

Look for flags:
- `myself,master` - This is a master node
- `slave` - This is a replica
- `fail` - Node is failing

---

## 6. Common Issues & Solutions

### Issue 1: Constant MOVED Errors

**Cause:** Client's slot cache is outdated.

**Solution:**
```bash
# In go-redis, refresh the slot mapping
client.ReloadState(ctx)
```

### Issue 2: ASK Errors During Resharding

**Cause:** Normal behavior during slot migration.

**Solution:**
- Let the cluster client handle it automatically
- Or manually send `ASKING` before commands

### Issue 3: CLUSTERDOWN Errors

**Cause:** Not all 16384 slots are covered.

**Solution:**
```bash
# Check slot coverage
docker exec redis-1 redis-cli -p 7001 CLUSTER INFO | grep cluster_slots

# Fix by resharding or adding nodes
```

---

## 7. Quick Reference

### MOVED Response Format

```
MOVED <slot> <ip>:<port>
MOVED 12182 172.30.0.13:7003
```

**Client should:**
1. Update internal slot → node mapping
2. Retry command on the specified node
3. Use new mapping for all future requests to this slot

### ASK Response Format

```
ASK <slot> <ip>:<port>
ASK 0 172.30.0.13:7003
```

**Client should:**
1. Send `ASKING` command to target node
2. Send original command to target node
3. Do NOT update slot mapping (migration may fail)

---

## 8. Summary

| Aspect | MOVED | ASK |
|--------|-------|-----|
| **Meaning** | Slot permanently moved | Key temporarily elsewhere |
| **When** | After resharding completes | During live migration |
| **Cache Update** | Yes - update slot mapping | No - migration may rollback |
| **Client Action** | Retry on new node | `ASKING` + retry |
| **Frequency** | Rare (after config changes) | Only during resharding |

### Key Takeaways

1. **MOVED** = Permanent redirect. Update your routing table.
2. **ASK** = Temporary redirect. Don't update routing, use `ASKING`.
3. Cluster-aware clients (like go-redis) handle both automatically.
4. During resharding, ASK errors are normal and expected.
5. Use `-c` flag with redis-cli for automatic redirect handling.

---

## Next Lab

Continue to [Lab 5: Failover & High Availability](./Part6-Lab-Exercises-5-8-Integration-Intro.md) to learn about automatic failover and recovery.
