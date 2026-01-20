# Lab 2: Hash Slots & Key Distribution

## Overview

This lab teaches how Redis Cluster determines where each key is stored. Understanding hash slots and key distribution is essential for designing efficient Redis Cluster applications.

---

## 1. The CRC16 Hashing Algorithm

Redis Cluster uses **CRC16 hashing** to map keys to slots.

### How It Works

```
Key → CRC16(key) → result mod 16384 → Slot Number (0-16383)
```

### Formula

```
SLOT = CRC16(key) % 16384
```

### Example Calculations

| Key | CRC16 Value | Slot (mod 16384) |
|-----|-------------|------------------|
| `user:1` | 14525 | 14525 |
| `user:2` | 14014 | 14014 |
| `order:1` | 8127 | 8127 |
| `product:abc` | 3892 | 3892 |

**Key Insight:** Different keys hash to different slots, distributing data across nodes automatically.

---

## 2. Slot-to-Node Mapping

### The 16,384 Slots

Redis Cluster divides all data into exactly **16,384 hash slots** (0 to 16,383).

```
┌─────────────────────────────────────────────────────────────────────┐
│                    16,384 HASH SLOTS                                 │
├───────────────────┬───────────────────┬───────────────────┬─────────┤
│   Slots 0-5460    │  Slots 5461-10922 │ Slots 10923-16383 │         │
│   (Master 1)      │   (Master 2)      │   (Master 3)      │         │
│   5,461 slots     │   5,462 slots     │   5,461 slots     │         │
└───────────────────┴───────────────────┴───────────────────┴─────────┘
```

### Slot Distribution with 3 Masters

| Master | Port | Slot Range | Count |
|--------|------|------------|-------|
| Master 1 | 7001 | 0-5460 | 5,461 |
| Master 2 | 7002 | 5461-10922 | 5,462 |
| Master 3 | 7003 | 10923-16383 | 5,461 |

**Total: 16,384 slots = complete coverage**

---

## 3. Lab Commands Reference

### Check Slot for Any Key

```bash
make key-slot KEY="user:1001"
```

**Output:**
```
┌──────────────────────────────────────────────────────────────────┐
│                    KEY TO SLOT MAPPING                           │
├──────────────────────────────────────────────────────────────────┤
│  Key: user:1001                                                  │
│  Slot: 12539  Node: 172.30.0.13:7003                             │
└──────────────────────────────────────────────────────────────────┘
```

### View Slot Distribution

```bash
make slot-info
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              REDIS CLUSTER SLOT DISTRIBUTION                     ║
╠══════════════════════════════════════════════════════════════════╣
║  Total Slots: 16384    Cluster Size: 3 masters                   ║
╠══════════════════════════════════════════════════════════════════╣
║  Node             Slots      %      Bar                          ║
║  172.30.0.11:7001  5461   33.3%   ██████░░░░░░░░░░░░░░           ║
║  172.30.0.12:7002  5462   33.3%   ██████░░░░░░░░░░░░░░           ║
║  172.30.0.13:7003  5461   33.3%   ██████░░░░░░░░░░░░░░           ║
╚══════════════════════════════════════════════════════════════════╝
```

### Using redis-cli Directly

```bash
# Calculate slot for a key
redis-cli -c -p 7001 CLUSTER KEYSLOT "user:1001"
# Output: (integer) 12539

# View all slot assignments
redis-cli -c -p 7001 CLUSTER SLOTS
```

---

## 4. The Problem: Related Keys on Different Nodes

### Without Hash Tags

```bash
make key-slot KEY="event:123"
make key-slot KEY="event:123:seats"
make key-slot KEY="event:123:waitlist"
```

**Result:**
```
event:123           → Slot 10456  → Master 2
event:123:seats     → Slot 3892   → Master 1
event:123:waitlist  → Slot 15234  → Master 3
```

**Problem:** Related data scattered across 3 different nodes!

### Why This Matters

- Cannot use `MULTI/EXEC` transactions across nodes
- Cannot use Lua scripts on keys from different slots
- Cannot use multi-key commands like `MGET`, `MSET`

---

## 5. The Solution: Hash Tags

### Hash Tag Syntax

```
{hashtag}:rest:of:key
```

Only the content inside `{}` is hashed to determine the slot.

### With Hash Tags

```bash
make key-slot KEY="{event:123}"
make key-slot KEY="{event:123}:seats"
make key-slot KEY="{event:123}:waitlist"
```

**Result:**
```
{event:123}           → Slot 10456  → Master 2
{event:123}:seats     → Slot 10456  → Master 2
{event:123}:waitlist  → Slot 10456  → Master 2
```

**All keys on the SAME slot!**

---

## 6. Hash Tag Demo

```bash
make hash-tag-demo
```

### Demo Output

```
╔══════════════════════════════════════════════════════════════════╗
║                   HASH TAG DEMONSTRATION                         ║
╠══════════════════════════════════════════════════════════════════╣
║  Hash tags {} control which part of the key determines the slot  ║
╚══════════════════════════════════════════════════════════════════╝

┌── WITHOUT HASH TAGS ──────────────────────────────────────────────┐
│  user:1001             → Slot 12539 → 172.30.0.13:7003
│  user:1002             → Slot  5649 → 172.30.0.12:7002
│  user:1003             → Slot  1440 → 172.30.0.11:7001
│  order:5001            → Slot  8127 → 172.30.0.12:7002
│  (Keys distributed across different slots/nodes)
└───────────────────────────────────────────────────────────────────┘

┌── WITH HASH TAGS (Same user) ────────────────────────────────────┐
│  {user:1001}:profile   → Slot 12539 → 172.30.0.13:7003
│  {user:1001}:orders    → Slot 12539 → 172.30.0.13:7003
│  {user:1001}:cart      → Slot 12539 → 172.30.0.13:7003
│  {user:1001}:wishlist  → Slot 12539 → 172.30.0.13:7003
│  (All keys on SAME slot - can use MULTI/Lua together!)
└───────────────────────────────────────────────────────────────────┘
```

---

## 7. Cross-Slot Limitations

### Cross-Slot Error Example

```bash
# This command will fail
redis-cli -c -p 7001 MGET user:1 user:2 user:3
```

**Error:**
```
(error) CROSSSLOT Keys in request don't hash to the same slot
```

### Solution with Hash Tags

```bash
# This works - all keys share same hash tag
redis-cli -c -p 7001 MSET "{user:1}:name" "Alice" "{user:1}:email" "alice@test.com"
redis-cli -c -p 7001 MGET "{user:1}:name" "{user:1}:email"
```

### Cross-Slot Demo

```bash
make cross-slot-demo
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              CROSS-SLOT OPERATIONS DEMONSTRATION                  ║
╠══════════════════════════════════════════════════════════════════╣
║  Multi-key operations require keys to be in the same slot        ║
╚══════════════════════════════════════════════════════════════════╝

┌── LUA SCRIPT LIMITATIONS ────────────────────────────────────────┐
│  Lua scripts can only access keys in a SINGLE slot
│
│  Same-slot Lua: SUCCESS - [value3, value4]
│  Cross-slot Lua: ERROR - CROSSSLOT Keys don't hash to same slot
│
│  Solution: Use hash tags to co-locate related keys!
└───────────────────────────────────────────────────────────────────┘
```

---

## 8. Hash Tag Patterns

### Common Patterns

| Use Case | Pattern | Example Keys |
|----------|---------|--------------|
| User data | `{user:ID}:field` | `{user:42}:profile`, `{user:42}:cart` |
| Order data | `{order:ID}:field` | `{order:99}:items`, `{order:99}:status` |
| Event data | `{event:ID}:field` | `{event:abc}:seats`, `{event:abc}:waitlist` |
| Session | `{session:ID}:field` | `{session:xyz}:data`, `{session:xyz}:expiry` |

### Ticket Reservation System Pattern

```
{event:concert123}              # Event metadata
{event:concert123}:seats        # Seat availability (Hash)
{event:concert123}:stats        # Statistics (Hash)
{event:concert123}:reservations # Active reservations (Set)
{event:concert123}:waitlist     # Waitlist (Sorted Set)
```

**All event data co-located for atomic Lua script operations!**

---

## 9. Atomic Operations with Co-located Keys

### Example: Update Profile AND Cart Atomically

**Without hash tags (FAILS):**
```go
profileKey := "profile:user_42"    // Slot 8234
cartKey := "cart:user_42"          // Slot 15678
// Cannot use Lua script - different slots!
```

**With hash tags (WORKS):**
```go
profileKey := "{user:42}:profile"  // Slot 5129
cartKey := "{user:42}:cart"        // Slot 5129
// Same slot - Lua script works!
```

### Lua Script Example

```lua
-- This works because both keys share hash tag {user:42}
local profile_key = KEYS[1]  -- {user:42}:profile
local cart_key = KEYS[2]     -- {user:42}:cart

-- Atomic: Add points AND clear cart
local points = redis.call('HGET', profile_key, 'points')
redis.call('HSET', profile_key, 'points', points + 50)
redis.call('DEL', cart_key)

return points + 50
```

---

## 10. Key Distribution Analysis

### Analyze Existing Keys

```bash
make analyze-distribution --pattern "*" --limit 1000
```

**Output:**
```
╔══════════════════════════════════════════════════════════════════╗
║              KEY DISTRIBUTION ANALYSIS                            ║
║  Pattern: *                    Limit: 1000                        ║
╚══════════════════════════════════════════════════════════════════╝

┌── DISTRIBUTION BY NODE ──────────────────────────────────────────┐
│  172.30.0.11:7001   334 keys (33.4%) ██████░░░░░░░░░░░░░░        │
│  172.30.0.12:7002   331 keys (33.1%) ██████░░░░░░░░░░░░░░        │
│  172.30.0.13:7003   335 keys (33.5%) ██████░░░░░░░░░░░░░░        │
└───────────────────────────────────────────────────────────────────┘

┌── HOT SPOTS (Slots with most keys) ─────────────────────────────┐
│  Slot  5129:   45 keys (Node: 172.30.0.11:7001)
│  Slot 10456:   38 keys (Node: 172.30.0.12:7002)
│  Slot 12539:   22 keys (Node: 172.30.0.13:7003)
└───────────────────────────────────────────────────────────────────┘
```

### What to Look For

- **Even distribution** across nodes (~33% each with 3 masters)
- **Hot spots** may indicate heavy hash tag usage (expected for related data)
- **Imbalanced distribution** may require key pattern review

---

## 11. Best Practices

### DO: Use Hash Tags for Related Data

```
{user:123}:profile
{user:123}:settings
{user:123}:notifications
```

### DO: Keep Hash Tags Consistent

```
# Good - consistent pattern
{order:456}:items
{order:456}:payment
{order:456}:shipping

# Bad - inconsistent
order:456:items
{order:456}:payment
order:{456}:shipping
```

### DON'T: Put Everything in One Hash Tag

```
# Bad - all data on one node (defeats clustering purpose)
{global}:users
{global}:orders
{global}:products
```

### DON'T: Use Hash Tags for Unrelated Data

```
# Bad - unrelated data forced to same slot
{app}:user:1
{app}:product:5000
{app}:order:9999
```

---

## 12. Summary

### Key Concepts

| Concept | Description |
|---------|-------------|
| **CRC16 Hash** | Algorithm that converts key to number |
| **Hash Slot** | CRC16(key) % 16384 = slot (0-16383) |
| **Hash Tag** | `{tag}` portion determines slot |
| **Co-location** | Related keys on same slot via hash tags |
| **CROSSSLOT** | Error when multi-key ops span slots |

### Commands Learned

| Command | Purpose |
|---------|---------|
| `make key-slot KEY=x` | Find slot for a key |
| `make slot-info` | View slot distribution |
| `make hash-tag-demo` | See hash tags in action |
| `make cross-slot-demo` | Understand limitations |
| `make analyze-distribution` | Analyze key spread |

### Hash Tag Rules

1. Only content inside first `{...}` is hashed
2. Empty tags `{}` use entire key
3. Nested tags use outermost: `{{a}}` hashes `{a}`
4. No tag means entire key is hashed

---

## 13. Hands-On Exercises

### Exercise 1: Verify Slot Calculation

```bash
# Calculate slots for these keys
make key-slot KEY="product:1"
make key-slot KEY="product:2"
make key-slot KEY="{product}:1"
make key-slot KEY="{product}:2"
```

**Question:** Which pairs share the same slot?

### Exercise 2: Design a Schema

Design key patterns for a shopping cart system:
- User profile
- User's cart items
- User's order history
- User's wishlist

**Requirement:** All user data must support atomic operations.

### Exercise 3: Test Cross-Slot

```bash
# Try this and observe the behavior
redis-cli -c -p 7001 MSET key1 "a" key2 "b" key3 "c"
redis-cli -c -p 7001 MSET "{test}:1" "a" "{test}:2" "b" "{test}:3" "c"
```

---

## 14. Troubleshooting

### CROSSSLOT Error

**Problem:** `CROSSSLOT Keys in request don't hash to the same slot`

**Solution:** Use hash tags to co-locate keys:
```
Before: user:1:profile, user:1:cart
After:  {user:1}:profile, {user:1}:cart
```

### Uneven Distribution

**Problem:** One node has significantly more keys

**Check:**
```bash
make analyze-distribution
```

**Solutions:**
- Review hash tag usage
- Ensure hash tags represent entity IDs, not global values
- Consider resharding if slot distribution is uneven

### Keys Not Found After Resharding

**Problem:** Client caches old slot→node mapping

**Solution:** Smart clients (like go-redis) handle MOVED redirects automatically. If using raw connections, handle MOVED/ASK responses.

---

## Next Lab

**Lab 3: Atomic Operations with Lua** - Learn how to use Lua scripts for atomic multi-key operations in Redis Cluster.

```bash
# Preview Lab 3
make demo
make sharding-demo
```
