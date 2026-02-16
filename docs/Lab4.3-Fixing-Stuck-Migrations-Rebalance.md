# Lab 4.2: Fixing Stuck Migrations & Rebalancing

## Overview

This lab teaches how to diagnose and fix stuck slot migrations in Redis Cluster. Stuck migrations can occur when resharding is interrupted, and must be resolved before performing cluster operations like rebalancing.

---

## 1. Understanding the Problem

### What Causes Stuck Migrations?

Slot migrations can get stuck when:
- Network interruption during resharding
- Node crash during migration
- Manual resharding was interrupted (Ctrl+C)
- `redis-cli --cluster reshard` failed mid-operation

### Symptoms

When you try to rebalance or reshard:

```bash
docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 --cluster-use-empty-masters
```

**Error Output:**
```
>>> Performing Cluster Check (using node 172.30.0.11:7001)
[OK] All nodes agree about slots configuration.
>>> Check for open slots...
[WARNING] Node 172.30.0.17:7007 has slots in importing state 5519.
[WARNING] Node 172.30.0.12:7002 has slots in migrating state 5519.
[WARNING] The following slots are open: 5519
```

### What This Means

```
┌─────────────────────────────────────────────────────────────────────┐
│                     STUCK SLOT MIGRATION                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   Node 7002 (Source)               Node 7007 (Target)                │
│   ┌─────────────────┐              ┌─────────────────┐               │
│   │ MIGRATING       │   ────X────▶ │ IMPORTING       │               │
│   │ slot 5519       │   stuck!     │ slot 5519       │               │
│   │                 │              │                 │               │
│   │ [1 key stuck]   │              │ [0 keys]        │               │
│   └─────────────────┘              └─────────────────┘               │
│                                                                      │
│   Migration started but never completed.                             │
│   Slot is in limbo - owned by neither node properly.                 │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Diagnosing Stuck Slots

### Step 1: Check Cluster Status

```bash
docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
```

Look for warnings about:
- `slots in migrating state`
- `slots in importing state`
- `open slots`

### Step 2: Identify the Stuck Slot

```bash
# Check cluster nodes for migration flags
docker exec redis-1 redis-cli -p 7001 CLUSTER NODES
```

**Output shows migration state:**
```
72d98d79... 172.30.0.12:7002 master - 0 ... connected 5519-10922 [5519->-7c9d0954...]
                                                                   ↑
                                                          Migration flag: slot 5519
                                                          migrating to node 7c9d0954...
```

Migration flags format:
- `[5519->-<node-id>]` = MIGRATING (source node)
- `[5519-<-<node-id>]` = IMPORTING (target node)

### Step 3: Check Keys in Stuck Slot

```bash
# Count keys in slot on source node
docker exec redis-2 redis-cli -p 7002 CLUSTER COUNTKEYSINSLOT 5519

# Count keys in slot on target node
docker exec redis-7 redis-cli -p 7007 CLUSTER COUNTKEYSINSLOT 5519

# List actual keys (up to 100)
docker exec redis-2 redis-cli -p 7002 CLUSTER GETKEYSINSLOT 5519 100
```

### Step 4: Get Node IDs

```bash
# Source node ID
docker exec redis-2 redis-cli -p 7002 CLUSTER MYID
# Example: 72d98d793cbc0b042040b9168d9946a4d9c82566

# Target node ID
docker exec redis-7 redis-cli -p 7007 CLUSTER MYID
# Example: 7c9d09544a83b22a119ce4f926c4f8b3dc6f7ba9
```

---

## 3. Fixing Options

You have three options to fix stuck migrations:

| Option | When to Use | Result |
|--------|-------------|--------|
| **Auto Fix** | Most cases | Let Redis decide |
| **Complete Migration** | Keys mostly on target | Slot goes to target |
| **Cancel Migration** | Keys mostly on source | Slot stays on source |

---

## 4. Option 1: Automatic Fix (Recommended)

Redis can automatically fix most stuck migrations:

```bash
docker exec redis-1 redis-cli --cluster fix 172.30.0.11:7001
```

**What it does:**
1. Detects open/stuck slots
2. Migrates remaining keys
3. Finalizes slot ownership
4. Clears migration flags

**Example Output:**
```
>>> Performing Cluster Check (using node 172.30.0.11:7001)
[OK] All nodes agree about slots configuration.
>>> Check for open slots...
[WARNING] Node 172.30.0.17:7007 has slots in importing state 5519.
[WARNING] Node 172.30.0.12:7002 has slots in migrating state 5519.
>>> Fixing open slot 5519
Set as migrating in: 172.30.0.12:7002
Set as importing in: 172.30.0.17:7007
Moving slot 5519 from 172.30.0.12:7002 to 172.30.0.17:7007
>>> Check slots coverage...
[OK] All 16384 slots covered.
```

---

## 5. Option 2: Complete Migration Manually

Use when you want to finish the migration to the target node.

### Step-by-Step Process

```bash
# Variables (replace with your actual node IDs)
SOURCE_NODE="72d98d793cbc0b042040b9168d9946a4d9c82566"  # 7002
TARGET_NODE="7c9d09544a83b22a119ce4f926c4f8b3dc6f7ba9"  # 7007
STUCK_SLOT=5519
```

#### Step 1: Find Remaining Keys

```bash
docker exec redis-2 redis-cli -p 7002 CLUSTER GETKEYSINSLOT 5519 100
```

**Output:**
```
1) "user:loadtest_user_33:reservations"
```

#### Step 2: Migrate Each Key

```bash
# Migrate key from source (7002) to target (7007)
docker exec redis-2 redis-cli -p 7002 MIGRATE 172.30.0.17 7007 "user:loadtest_user_33:reservations" 0 5000
```

**Parameters:**
- `172.30.0.17` - Target IP
- `7007` - Target port
- `"key"` - Key to migrate
- `0` - Database number
- `5000` - Timeout in ms

**For multiple keys, use KEYS option:**
```bash
docker exec redis-2 redis-cli -p 7002 MIGRATE 172.30.0.17 7007 "" 0 5000 KEYS key1 key2 key3
```

#### Step 3: Verify No Keys Remain

```bash
docker exec redis-2 redis-cli -p 7002 CLUSTER COUNTKEYSINSLOT 5519
# Should output: 0
```

#### Step 4: Finalize Slot Ownership

Notify ALL nodes that target now owns the slot:

```bash
# Must run on all master nodes
docker exec redis-1 redis-cli -p 7001 CLUSTER SETSLOT 5519 NODE $TARGET_NODE
docker exec redis-2 redis-cli -p 7002 CLUSTER SETSLOT 5519 NODE $TARGET_NODE
docker exec redis-3 redis-cli -p 7003 CLUSTER SETSLOT 5519 NODE $TARGET_NODE
docker exec redis-7 redis-cli -p 7007 CLUSTER SETSLOT 5519 NODE $TARGET_NODE
```

#### Step 5: Verify Fix

```bash
docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
```

---

## 6. Option 3: Cancel Migration (Rollback)

Use when you want to abort the migration and keep the slot on the source node.

### Step-by-Step Process

#### Step 1: Migrate Keys Back to Source (if any moved)

```bash
# Check if any keys are on target
docker exec redis-7 redis-cli -p 7007 CLUSTER GETKEYSINSLOT 5519 100

# If keys exist, migrate them back
docker exec redis-7 redis-cli -p 7007 MIGRATE 172.30.0.12 7002 "keyname" 0 5000
```

#### Step 2: Reset Slot to Source Node

```bash
SOURCE_NODE="72d98d793cbc0b042040b9168d9946a4d9c82566"  # 7002

# Notify all nodes that source keeps the slot
docker exec redis-1 redis-cli -p 7001 CLUSTER SETSLOT 5519 NODE $SOURCE_NODE
docker exec redis-2 redis-cli -p 7002 CLUSTER SETSLOT 5519 NODE $SOURCE_NODE
docker exec redis-3 redis-cli -p 7003 CLUSTER SETSLOT 5519 NODE $SOURCE_NODE
docker exec redis-7 redis-cli -p 7007 CLUSTER SETSLOT 5519 NODE $SOURCE_NODE
```

#### Step 3: Verify

```bash
docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
```

---

## 7. After Fixing: Rebalance the Cluster

Once stuck slots are fixed, you can rebalance:

```bash
docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 --cluster-use-empty-masters
```

### Rebalance Options

| Option | Description |
|--------|-------------|
| `--cluster-use-empty-masters` | Include masters with 0 slots |
| `--cluster-weight <node-id>=<weight>` | Set relative weight for node |
| `--cluster-simulate` | Show plan without executing |
| `--cluster-timeout <ms>` | Migration timeout |
| `--cluster-pipeline <n>` | Keys per migration batch |

### Example: Weighted Rebalance

Give node 7007 double the slots:

```bash
docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 \
    --cluster-weight 7c9d09544a83b22a119ce4f926c4f8b3dc6f7ba9=2
```

### Example: Simulate First

```bash
docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 \
    --cluster-use-empty-masters \
    --cluster-simulate
```

---

## 8. Verifying Cluster Health

### Quick Health Check

```bash
docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
```

**Healthy Output:**
```
172.30.0.11:7001 (a4db5d78...) -> 12 keys | 4096 slots | 1 slaves.
172.30.0.17:7007 (7c9d0954...) -> 14 keys | 4096 slots | 0 slaves.
172.30.0.12:7002 (72d98d79...) -> 25 keys | 4096 slots | 1 slaves.
172.30.0.13:7003 (58760276...) -> 12 keys | 4096 slots | 1 slaves.
[OK] 63 keys in 4 masters.
[OK] All nodes agree about slots configuration.
>>> Check for open slots...
>>> Check slots coverage...
[OK] All 16384 slots covered.
```

### Detailed Cluster Info

```bash
docker exec redis-1 redis-cli -p 7001 CLUSTER INFO
```

**Key metrics to check:**
```
cluster_state:ok                 # Must be "ok"
cluster_slots_assigned:16384     # Must be 16384
cluster_slots_ok:16384           # Must be 16384
cluster_slots_pfail:0            # Should be 0
cluster_slots_fail:0             # Should be 0
```

---

## 9. Complete Fix Script

Here's a script to fix stuck slot 5519 (customize as needed):

```bash
#!/bin/bash
# fix-stuck-slot.sh

STUCK_SLOT=5519
SOURCE_PORT=7002
TARGET_PORT=7007
TARGET_IP="172.30.0.17"

echo "=== Fixing Stuck Slot $STUCK_SLOT ==="

# Get node IDs
SOURCE_NODE=$(docker exec redis-2 redis-cli -p $SOURCE_PORT CLUSTER MYID)
TARGET_NODE=$(docker exec redis-7 redis-cli -p $TARGET_PORT CLUSTER MYID)

echo "Source Node ($SOURCE_PORT): $SOURCE_NODE"
echo "Target Node ($TARGET_PORT): $TARGET_NODE"

# Get stuck keys
echo ""
echo "Keys in slot $STUCK_SLOT on source:"
KEYS=$(docker exec redis-2 redis-cli -p $SOURCE_PORT CLUSTER GETKEYSINSLOT $STUCK_SLOT 100)
echo "$KEYS"

# Migrate each key
echo ""
echo "Migrating keys..."
for KEY in $KEYS; do
    echo "  Migrating: $KEY"
    docker exec redis-2 redis-cli -p $SOURCE_PORT MIGRATE $TARGET_IP $TARGET_PORT "$KEY" 0 5000
done

# Verify migration
echo ""
echo "Keys remaining: $(docker exec redis-2 redis-cli -p $SOURCE_PORT CLUSTER COUNTKEYSINSLOT $STUCK_SLOT)"

# Finalize slot ownership
echo ""
echo "Finalizing slot ownership..."
for PORT in 7001 7002 7003 7007; do
    docker exec redis-1 redis-cli -p $PORT CLUSTER SETSLOT $STUCK_SLOT NODE $TARGET_NODE 2>/dev/null || true
done

# Verify
echo ""
echo "=== Verification ==="
docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001 | grep -E "slots|OK|WARNING"
```

---

## 10. Troubleshooting

### Issue: "CLUSTERDOWN" After Fix

**Cause:** Slots not properly assigned.

**Solution:**
```bash
# Check slot coverage
docker exec redis-1 redis-cli -p 7001 CLUSTER INFO | grep cluster_slots

# If slots missing, run fix again
docker exec redis-1 redis-cli --cluster fix 172.30.0.11:7001
```

### Issue: "CROSSSLOT" Errors After Rebalance

**Cause:** Multi-key operations span different slots.

**Solution:** Use hash tags to ensure related keys are on same slot:
```bash
# These will be on the same slot
SET {user:123}:profile "data"
SET {user:123}:settings "data"
```

### Issue: Rebalance Takes Too Long

**Cause:** Many keys to migrate.

**Solution:** Increase pipeline size:
```bash
docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 \
    --cluster-pipeline 100 \
    --cluster-use-empty-masters
```

### Issue: Migration Fails with "BUSYKEY"

**Cause:** Key already exists on target.

**Solution:** Use REPLACE option:
```bash
docker exec redis-2 redis-cli -p 7002 MIGRATE 172.30.0.17 7007 "keyname" 0 5000 REPLACE
```

---

## 11. Summary

### Fix Stuck Migration Checklist

1. **Diagnose**
   ```bash
   docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
   ```

2. **Try Auto Fix**
   ```bash
   docker exec redis-1 redis-cli --cluster fix 172.30.0.11:7001
   ```

3. **If Auto Fix Fails, Manual Fix:**
   - Find stuck keys: `CLUSTER GETKEYSINSLOT`
   - Migrate keys: `MIGRATE`
   - Finalize: `CLUSTER SETSLOT ... NODE`

4. **Verify**
   ```bash
   docker exec redis-1 redis-cli --cluster check 172.30.0.11:7001
   ```

5. **Rebalance**
   ```bash
   docker exec redis-1 redis-cli --cluster rebalance 172.30.0.11:7001 --cluster-use-empty-masters
   ```

### Key Commands Reference

| Command | Purpose |
|---------|---------|
| `--cluster check` | Diagnose cluster issues |
| `--cluster fix` | Auto-fix stuck slots |
| `--cluster rebalance` | Redistribute slots evenly |
| `CLUSTER COUNTKEYSINSLOT` | Count keys in a slot |
| `CLUSTER GETKEYSINSLOT` | List keys in a slot |
| `MIGRATE` | Move key to another node |
| `CLUSTER SETSLOT ... NODE` | Finalize slot ownership |

---

## Next Lab

Continue to [Lab 6: Failover & High Availability](./Part6-Lab-Exercises-5-8-Integration-Intro.md) to learn about automatic failover and replica promotion.
