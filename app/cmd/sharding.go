package cmd

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ticket-reservation/cluster"

	"github.com/redis/go-redis/v9"
)

// SlotInfo displays slot distribution across the cluster
func SlotInfo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	info, err := client.GetClusterInfo()
	if err != nil {
		return err
	}

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              REDIS CLUSTER SLOT DISTRIBUTION                     ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Slots: 16384    Cluster Size: %-3d masters                 ║\n", info.Size)
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")

	// Calculate slots per node
	type nodeSlots struct {
		address string
		slots   int
		ranges  string
	}

	var masters []nodeSlots
	for _, node := range info.Nodes {
		if node.Role != "master" {
			continue
		}

		slotCount := 0
		if node.Slots != "" {
			ranges := strings.Fields(node.Slots)
			for _, r := range ranges {
				if strings.Contains(r, "-") {
					parts := strings.Split(r, "-")
					if len(parts) == 2 {
						var start, end int
						fmt.Sscanf(parts[0], "%d", &start)
						fmt.Sscanf(parts[1], "%d", &end)
						slotCount += end - start + 1
					}
				} else {
					slotCount++
				}
			}
		}

		masters = append(masters, nodeSlots{
			address: node.Address,
			slots:   slotCount,
			ranges:  node.Slots,
		})
	}

	// Sort by address
	sort.Slice(masters, func(i, j int) bool {
		return masters[i].address < masters[j].address
	})

	idealSlots := 16384 / len(masters)
	fmt.Println("║                                                                  ║")
	fmt.Println("║  Node             Slots      %      Bar                          ║")
	fmt.Println("║  ────             ─────      ─      ───                          ║")

	for _, m := range masters {
		pct := float64(m.slots) / 16384.0 * 100
		barLen := int(pct / 5) // 20 chars max
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", 20-barLen)
		fmt.Printf("║  %-16s %5d   %5.1f%%   %s  	   ║\n", m.address, m.slots, pct, bar)
	}

	fmt.Println("║                                                                  ║")
	fmt.Printf("║  Ideal distribution: ~%d slots per master                      ║\n", idealSlots)
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Show slot ranges
	fmt.Println("\n┌──────────────────────────────────────────────────────────────────┐")
	fmt.Println("│                    SLOT RANGE ASSIGNMENTS                        │")
	fmt.Println("├──────────────────────────────────────────────────────────────────┤")
	for _, m := range masters {
		fmt.Printf("│  %-16s → %s\n", m.address, m.ranges)
	}
	fmt.Println("└──────────────────────────────────────────────────────────────────┘")

	return nil
}

// KeySlot shows which slot and node a key maps to
func KeySlot(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("key required")
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("\n┌──────────────────────────────────────────────────────────────────┐")
	fmt.Println("│                    KEY TO SLOT MAPPING                           │")
	fmt.Println("├──────────────────────────────────────────────────────────────────┤")

	for _, key := range args {
		slot := client.GetSlotForKey(key)
		nodeAddr, _ := client.GetNodeForSlot(slot)

		// Calculate hash tag if present
		hashPart := extractHashTag(key)

		fmt.Printf("│  Key: %-50s 	   │\n", key)
		if hashPart != key {
			fmt.Printf("│  Hash Tag: {%s}\n", hashPart)
		}
		fmt.Printf("│  Slot: %-5d  Node: %-20s                  	   │\n", slot, nodeAddr)
		fmt.Println("├──────────────────────────────────────────────────────────────────┤")
	}
	fmt.Println("└──────────────────────────────────────────────────────────────────┘")

	return nil
}

// extractHashTag extracts the hash tag portion from a key
func extractHashTag(key string) string {
	start := strings.Index(key, "{")
	if start == -1 {
		return key
	}
	end := strings.Index(key[start:], "}")
	if end == -1 {
		return key
	}
	tag := key[start+1 : start+end]
	if tag == "" {
		return key
	}
	return tag
}

// HashTagDemo demonstrates how hash tags work
func HashTagDemo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                   HASH TAG DEMONSTRATION                         ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Hash tags {} control which part of the key determines the slot  ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Demo 1: Keys without hash tags
	fmt.Println("\n┌── WITHOUT HASH TAGS ──────────────────────────────────────────────┐")
	keys1 := []string{"user:1001", "user:1002", "user:1003", "order:5001", "order:5002"}
	for _, key := range keys1 {
		slot := client.GetSlotForKey(key)
		node, _ := client.GetNodeForSlot(slot)
		fmt.Printf("│  %-20s → Slot %5d → %s\n", key, slot, node)
	}
	fmt.Println("│  (Keys distributed across different slots/nodes)")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Demo 2: Keys with same hash tag
	fmt.Println("\n┌── WITH HASH TAGS (Same user) ────────────────────────────────────┐")
	keys2 := []string{"{user:1001}:profile", "{user:1001}:orders", "{user:1001}:cart", "{user:1001}:wishlist"}
	for _, key := range keys2 {
		slot := client.GetSlotForKey(key)
		node, _ := client.GetNodeForSlot(slot)
		fmt.Printf("│  %-30s → Slot %5d → %s\n", key, slot, node)
	}
	fmt.Println("│  (All keys on SAME slot - can use MULTI/Lua together!)")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Demo 3: Event-based hash tags (like our reservation system)
	fmt.Println("\n┌── RESERVATION SYSTEM PATTERN ────────────────────────────────────┐")
	eventID := "concert123"
	keys3 := []string{
		fmt.Sprintf("{event:%s}", eventID),
		fmt.Sprintf("{event:%s}:seats", eventID),
		fmt.Sprintf("{event:%s}:stats", eventID),
		fmt.Sprintf("{event:%s}:reservations", eventID),
		fmt.Sprintf("{event:%s}:waitlist", eventID),
	}
	for _, key := range keys3 {
		slot := client.GetSlotForKey(key)
		fmt.Printf("│  %-35s → Slot %5d\n", key, slot)
	}
	fmt.Println("│")
	fmt.Println("│  All event data co-located for atomic Lua script operations!")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Demonstrate actual writes
	fmt.Println("\n┌── LIVE DEMONSTRATION ────────────────────────────────────────────┐")
	ctx := context.Background()
	rdb := client.Redis()

	// Write related keys with hash tag
	demoEvent := fmt.Sprintf("demo_%d", time.Now().Unix())
	pipe := rdb.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("{event:%s}", demoEvent), `{"name":"Demo Event"}`, time.Minute)
	pipe.HSet(ctx, fmt.Sprintf("{event:%s}:seats", demoEvent), "A1", "available", "A2", "available")
	pipe.HSet(ctx, fmt.Sprintf("{event:%s}:stats", demoEvent), "total", "100", "available", "100")
	_, err = pipe.Exec(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("│  Created demo event: %s\n", demoEvent)
	fmt.Println("│  Written 3 related keys to same slot using hash tag")
	fmt.Println("│")

	// Show the keys exist on same node
	slot := client.GetSlotForKey(fmt.Sprintf("{event:%s}", demoEvent))
	node, _ := client.GetNodeForSlot(slot)
	fmt.Printf("│  All keys on slot %d (node: %s)\n", slot, node)

	// Clean up
	pipe = rdb.Pipeline()
	pipe.Del(ctx, fmt.Sprintf("{event:%s}", demoEvent))
	pipe.Del(ctx, fmt.Sprintf("{event:%s}:seats", demoEvent))
	pipe.Del(ctx, fmt.Sprintf("{event:%s}:stats", demoEvent))
	pipe.Exec(ctx)

	fmt.Println("│  (Demo keys cleaned up)")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	return nil
}

// CrossSlotDemo demonstrates cross-slot operation limitations
func CrossSlotDemo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	rdb := client.Redis()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              CROSS-SLOT OPERATIONS DEMONSTRATION                 ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Multi-key operations require keys to be in the same slot        ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Setup test keys
	key1 := "crossdemo:key1"
	key2 := "crossdemo:key2"
	key3 := "{crossdemo}:key1"
	key4 := "{crossdemo}:key2"

	rdb.Set(ctx, key1, "value1", time.Minute)
	rdb.Set(ctx, key2, "value2", time.Minute)
	rdb.Set(ctx, key3, "value3", time.Minute)
	rdb.Set(ctx, key4, "value4", time.Minute)

	fmt.Println("\n┌── KEYS WITHOUT HASH TAGS ────────────────────────────────────────┐")
	slot1 := client.GetSlotForKey(key1)
	slot2 := client.GetSlotForKey(key2)
	fmt.Printf("│  %s → Slot %d\n", key1, slot1)
	fmt.Printf("│  %s → Slot %d\n", key2, slot2)
	fmt.Println("│")

	// Try MGET on different slots
	fmt.Println("│  Attempting MGET on keys in different slots...")
	vals, err := rdb.MGet(ctx, key1, key2).Result()
	if err != nil {
		fmt.Printf("│  ERROR: %v\n", err)
	} else {
		fmt.Printf("│  SUCCESS (client handles routing): %v\n", vals)
		fmt.Println("│  Note: go-redis automatically routes to multiple nodes")
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	fmt.Println("\n┌── KEYS WITH HASH TAGS ───────────────────────────────────────────┐")
	slot3 := client.GetSlotForKey(key3)
	slot4 := client.GetSlotForKey(key4)
	fmt.Printf("│  %s → Slot %d\n", key3, slot3)
	fmt.Printf("│  %s → Slot %d\n", key4, slot4)
	fmt.Println("│")

	vals, err = rdb.MGet(ctx, key3, key4).Result()
	if err != nil {
		fmt.Printf("│  ERROR: %v\n", err)
	} else {
		fmt.Printf("│  SUCCESS: %v\n", vals)
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Demonstrate Lua script limitation
	fmt.Println("\n┌── LUA SCRIPT LIMITATIONS ────────────────────────────────────────┐")
	fmt.Println("│  Lua scripts can only access keys in a SINGLE slot")
	fmt.Println("│")

	// This will work - same slot
	luaScript := redis.NewScript(`
		local val1 = redis.call('GET', KEYS[1])
		local val2 = redis.call('GET', KEYS[2])
		return {val1, val2}
	`)

	result, err := luaScript.Run(ctx, rdb, []string{key3, key4}).Result()
	if err != nil {
		fmt.Printf("│  Same-slot Lua: ERROR - %v\n", err)
	} else {
		fmt.Printf("│  Same-slot Lua: SUCCESS - %v\n", result)
	}

	// This will fail - different slots
	_, err = luaScript.Run(ctx, rdb, []string{key1, key2}).Result()
	if err != nil {
		fmt.Printf("│  Cross-slot Lua: ERROR (expected) - %v\n", err)
	} else {
		fmt.Println("│  Cross-slot Lua: Unexpected success")
	}

	fmt.Println("│")
	fmt.Println("│  Solution: Use hash tags to co-locate related keys!")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Cleanup
	rdb.Del(ctx, key1, key2, key3, key4)

	return nil
}

// AnalyzeDistribution analyzes key distribution across the cluster
func AnalyzeDistribution(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	pattern := fs.String("pattern", "*", "Key pattern to analyze")
	limit := fs.Int("limit", 1000, "Maximum keys to scan")
	fs.Parse(args)

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := client.Context()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              KEY DISTRIBUTION ANALYSIS                           ║")
	fmt.Printf("║  Pattern: %-20s  Limit: %-10d                ║\n", *pattern, *limit)
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Collect keys from all masters
	type keyInfo struct {
		key  string
		slot int
		node string
	}

	var keys []keyInfo
	var mu sync.Mutex
	scanned := 0

	err = client.ForEachMaster(func(c *redis.Client) error {
		iter := c.Scan(ctx, 0, *pattern, int64(*limit)).Iterator()
		for iter.Next(ctx) {
			mu.Lock()
			if scanned >= *limit {
				mu.Unlock()
				break
			}
			key := iter.Val()
			slot := client.GetSlotForKey(key)
			node, _ := client.GetNodeForSlot(slot)
			keys = append(keys, keyInfo{key: key, slot: slot, node: node})
			scanned++
			mu.Unlock()
		}
		return nil
	})

	if err != nil {
		return err
	}

	if len(keys) == 0 {
		fmt.Println("\nNo keys found matching pattern.")
		return nil
	}

	// Analyze distribution
	nodeCount := make(map[string]int)
	slotCount := make(map[int]int)
	for _, k := range keys {
		nodeCount[k.node]++
		slotCount[k.slot]++
	}

	fmt.Println("\n┌── DISTRIBUTION BY NODE ───────────────────────────────────────────┐")
	type nodeStat struct {
		node  string
		count int
	}
	var nodeStats []nodeStat
	for node, count := range nodeCount {
		nodeStats = append(nodeStats, nodeStat{node, count})
	}
	sort.Slice(nodeStats, func(i, j int) bool {
		return nodeStats[i].count > nodeStats[j].count
	})

	for _, ns := range nodeStats {
		pct := float64(ns.count) / float64(len(keys)) * 100
		bar := strings.Repeat("█", int(pct/5)) + strings.Repeat("░", 20-int(pct/5))
		fmt.Printf("│  %-16s %5d keys (%5.1f%%) %s   	    │\n", ns.node, ns.count, pct, bar)
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Hot spots (slots with many keys)
	fmt.Println("\n┌── HOT SPOTS (Slots with most keys) ───────────────────────────────┐")
	type slotStat struct {
		slot  int
		count int
	}
	var slotStats []slotStat
	for slot, count := range slotCount {
		slotStats = append(slotStats, slotStat{slot, count})
	}
	sort.Slice(slotStats, func(i, j int) bool {
		return slotStats[i].count > slotStats[j].count
	})

	shown := 0
	for _, ss := range slotStats {
		if shown >= 10 {
			break
		}
		node, _ := client.GetNodeForSlot(ss.slot)
		fmt.Printf("│  Slot %5d: %4d keys (Node: %s)\n", ss.slot, ss.count, node)
		shown++
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Sample keys
	fmt.Println("\n┌── SAMPLE KEYS ───────────────────────────────────────────────────┐")
	for i, k := range keys {
		if i >= 10 {
			fmt.Printf("│  ... and %d more keys\n", len(keys)-10)
			break
		}
		fmt.Printf("│  %-40s → Slot %5d\n", truncate(k.key, 40), k.slot)
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	fmt.Printf("\nTotal: %d keys across %d nodes, %d unique slots\n",
		len(keys), len(nodeCount), len(slotCount))

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ShardingDemo runs a comprehensive sharding demonstration
func ShardingDemo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	rdb := client.Redis()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              REDIS CLUSTER SHARDING DEMONSTRATION                 ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Part 1: Show CRC16 hashing
	fmt.Println("\n┌── PART 1: HOW SLOT ASSIGNMENT WORKS ─────────────────────────────┐")
	fmt.Println("│  Redis uses CRC16 hash of key (or hash tag) mod 16384")
	fmt.Println("│")

	testKeys := []string{"user:1", "user:2", "user:1000", "order:1", "product:abc"}
	for _, key := range testKeys {
		crc := crc16Hash(key)
		slot := crc % 16384
		node, _ := client.GetNodeForSlot(int(slot))
		fmt.Printf("│  %-15s CRC16=%5d  Slot=%5d  Node=%s\n", key, crc, slot, node)
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Part 2: Demonstrate data distribution
	fmt.Println("\n┌── PART 2: DATA DISTRIBUTION ─────────────────────────────────────┐")
	fmt.Println("│  Creating 100 keys to observe distribution...")

	nodeHits := make(map[string]int)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("shardtest:item:%d", i)
		rdb.Set(ctx, key, fmt.Sprintf("value_%d", i), time.Minute)

		slot := client.GetSlotForKey(key)
		node, _ := client.GetNodeForSlot(slot)
		nodeHits[node]++
	}

	fmt.Println("│")
	fmt.Println("│  Distribution of 100 random keys:")
	for node, count := range nodeHits {
		bar := strings.Repeat("█", count/2)
		fmt.Printf("│    %-16s: %3d keys %s\n", node, count, bar)
	}
	fmt.Println("│")
	fmt.Println("│  Keys naturally distribute across masters (≈33% each)")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Part 3: Sequential key problem
	fmt.Println("\n┌── PART 3: SEQUENTIAL KEY PATTERNS ───────────────────────────────┐")
	fmt.Println("│  Sequential IDs can create uneven distribution!")
	fmt.Println("│")

	// Show how sequential numeric keys distribute
	seqNodeHits := make(map[string]int)
	for i := 1; i <= 100; i++ {
		key := fmt.Sprintf("seq:%d", i)
		slot := client.GetSlotForKey(key)
		node, _ := client.GetNodeForSlot(slot)
		seqNodeHits[node]++
	}

	fmt.Println("│  Sequential keys (seq:1, seq:2, ..., seq:100):")
	for node, count := range seqNodeHits {
		bar := strings.Repeat("█", count/2)
		fmt.Printf("│    %-16s: %3d keys %s\n", node, count, bar)
	}

	// Show better approach with UUIDs
	fmt.Println("│")
	fmt.Println("│  Better: Use UUIDs or random prefixes for even distribution")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Part 4: Hash tags for related data
	fmt.Println("\n┌── PART 4: HASH TAGS FOR TRANSACTIONS ────────────────────────────┐")
	fmt.Println("│  Problem: Need to update user profile AND cart atomically")
	fmt.Println("│")

	userID := "user_42"

	// Without hash tags - different slots
	profileKey := fmt.Sprintf("profile:%s", userID)
	cartKey := fmt.Sprintf("cart:%s", userID)
	fmt.Printf("│  Without tags: %s (slot %d), %s (slot %d)\n",
		profileKey, client.GetSlotForKey(profileKey),
		cartKey, client.GetSlotForKey(cartKey))
	fmt.Println("│  → Cannot use MULTI/EXEC or Lua atomically!")

	// With hash tags - same slot
	profileKey2 := fmt.Sprintf("{user:%s}:profile", userID)
	cartKey2 := fmt.Sprintf("{user:%s}:cart", userID)
	fmt.Println("│")
	fmt.Printf("│  With tags: %s (slot %d), %s (slot %d)\n",
		profileKey2, client.GetSlotForKey(profileKey2),
		cartKey2, client.GetSlotForKey(cartKey2))
	fmt.Println("│  → Same slot! Can use atomic operations!")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Part 5: Demonstrate actual atomic operation
	fmt.Println("\n┌── PART 5: ATOMIC OPERATIONS WITH HASH TAGS ──────────────────────┐")

	// Setup keys
	rdb.HSet(ctx, profileKey2, "name", "Alice", "points", "100")
	rdb.HSet(ctx, cartKey2, "items", "3", "total", "150.00")

	// Atomic operation: Add points when order placed
	atomicScript := redis.NewScript(`
		local profile_key = KEYS[1]
		local cart_key = KEYS[2]
		local bonus_points = tonumber(ARGV[1])

		-- Get current points and add bonus
		local current = tonumber(redis.call('HGET', profile_key, 'points') or 0)
		redis.call('HSET', profile_key, 'points', current + bonus_points)

		-- Clear cart
		redis.call('DEL', cart_key)

		return current + bonus_points
	`)

	newPoints, err := atomicScript.Run(ctx, rdb, []string{profileKey2, cartKey2}, 50).Int()
	if err != nil {
		fmt.Printf("│  Error: %v\n", err)
	} else {
		fmt.Printf("│  Atomically: Added 50 points (new total: %d) AND cleared cart\n", newPoints)
		fmt.Println("│  Both operations succeeded or both would have failed!")
	}
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Cleanup
	fmt.Println("\n┌── CLEANUP ───────────────────────────────────────────────────────┐")
	var cleaned int

	// Clean up test keys
	err = client.ForEachMaster(func(c *redis.Client) error {
		iter := c.Scan(ctx, 0, "shardtest:*", 100).Iterator()
		for iter.Next(ctx) {
			c.Del(ctx, iter.Val())
			cleaned++
		}
		return nil
	})
	rdb.Del(ctx, profileKey2, cartKey2)
	cleaned += 2

	fmt.Printf("│  Cleaned up %d demo keys\n", cleaned)
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              SHARDING DEMO COMPLETE!                              ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	return nil
}

// crc16Hash calculates CRC16-CCITT hash (same algorithm Redis uses)
// This is the XMODEM variant used by Redis
func crc16Hash(key string) uint16 {
	crc := uint16(0)
	for i := 0; i < len(key); i++ {
		crc ^= uint16(key[i]) << 8
		for j := 0; j < 8; j++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// ReshardDemo demonstrates manual resharding
func ReshardDemo(args []string) error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              RESHARDING DEMONSTRATION                             ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  This demo shows how to manually move slots between nodes         ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Show current distribution
	fmt.Println("\n[Step 1] Current slot distribution:")
	SlotInfo()

	// Explain the reshard process
	fmt.Println("\n┌── RESHARDING EXPLAINED ──────────────────────────────────────────┐")
	fmt.Println("│")
	fmt.Println("│  Resharding moves hash slots (and their keys) between nodes.")
	fmt.Println("│")
	fmt.Println("│  Process:")
	fmt.Println("│  1. CLUSTER SETSLOT <slot> IMPORTING <source-node>")
	fmt.Println("│  2. CLUSTER SETSLOT <slot> MIGRATING <target-node>")
	fmt.Println("│  3. CLUSTER GETKEYSINSLOT <slot> <count>")
	fmt.Println("│  4. MIGRATE <host> <port> \"\" 0 5000 KEYS <key1> <key2> ...")
	fmt.Println("│  5. CLUSTER SETSLOT <slot> NODE <target-node>")
	fmt.Println("│")
	fmt.Println("│  During migration:")
	fmt.Println("│  - Reads: handled by source (ASKING redirect if needed)")
	fmt.Println("│  - Writes: new keys go to target (MOVED redirect)")
	fmt.Println("│")
	fmt.Println("│  The redis-cli --cluster reshard command automates this!")
	fmt.Println("│")
	fmt.Println("│  Example command:")
	fmt.Println("│  redis-cli --cluster reshard localhost:7001 \\")
	fmt.Println("│    --cluster-from <source-id> \\")
	fmt.Println("│    --cluster-to <target-id> \\")
	fmt.Println("│    --cluster-slots 1000 \\")
	fmt.Println("│    --cluster-yes")
	fmt.Println("│")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Show commands to run
	nodes, _ := client.GetClusterNodes()
	var masters []string
	for _, n := range nodes {
		if n.Role == "master" {
			masters = append(masters, fmt.Sprintf("%s (%s)", n.Address, n.ID[:8]))
		}
	}

	fmt.Println("\n┌── TRY IT YOURSELF ───────────────────────────────────────────────┐")
	fmt.Println("│  Current masters:")
	for _, m := range masters {
		fmt.Printf("│    %s\n", m)
	}
	fmt.Println("│")
	fmt.Println("│  To move 500 slots from first to second master:")
	fmt.Println("│  make reshard-slots FROM=<id1> TO=<id2> SLOTS=500")
	fmt.Println("│")
	fmt.Println("│  Or use redis-cli directly:")
	fmt.Println("│  docker exec redis-1 redis-cli --cluster reshard redis-1:7001")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	return nil
}

// SimulateHotKey demonstrates hot key detection
func SimulateHotKey(args []string) error {
	fs := flag.NewFlagSet("hotkey", flag.ExitOnError)
	duration := fs.Int("duration", 5, "Duration in seconds")
	fs.Parse(args)

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	rdb := client.Redis()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              HOT KEY SIMULATION                                   ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	hotKey := "hotkey:product:popular"
	slot := client.GetSlotForKey(hotKey)
	node, _ := client.GetNodeForSlot(slot)

	fmt.Printf("\n  Hot key: %s\n", hotKey)
	fmt.Printf("  Slot: %d  Node: %s\n", slot, node)
	fmt.Printf("  Simulating heavy read load for %d seconds...\n\n", *duration)

	// Set up the hot key
	rdb.Set(ctx, hotKey, `{"id":"popular","name":"Popular Product","price":99.99}`, 5*time.Minute)

	// Simulate heavy reads
	var ops int64
	var wg sync.WaitGroup
	done := make(chan bool)

	// Launch concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					rdb.Get(ctx, hotKey)
					atomic.AddInt64(&ops, 1)
				}
			}
		}()
	}

	// Run for specified duration
	time.Sleep(time.Duration(*duration) * time.Second)
	close(done)
	wg.Wait()

	opsPerSec := float64(ops) / float64(*duration)

	fmt.Println("┌── RESULTS ───────────────────────────────────────────────────────┐")
	fmt.Printf("│  Total operations: %d\n", ops)
	fmt.Printf("│  Operations/sec:   %.0f\n", opsPerSec)
	fmt.Println("│")
	fmt.Println("│  Hot Key Solutions:")
	fmt.Println("│  1. Read replicas: Route reads to replicas (READONLY)")
	fmt.Println("│  2. Local caching: Cache in application memory")
	fmt.Println("│  3. Key splitting: {product:popular}:shard:1, :shard:2, etc.")
	fmt.Println("│  4. Client-side caching: Redis 6.0+ RESP3 protocol")
	fmt.Println("└───────────────────────────────────────────────────────────────────┘")

	// Cleanup
	rdb.Del(ctx, hotKey)

	return nil
}

// MigrationDemo shows what happens during key migration
func MigrationDemo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              KEY MIGRATION DURING RESHARDING                      ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	fmt.Println(`
  When slots are being migrated between nodes:

  ┌─────────────┐                      ┌─────────────┐
  │   Source    │  ───── MIGRATE ───→  │   Target    │
  │   Master    │                      │   Master    │
  │  (slot 100) │                      │  (slot 100) │
  └─────────────┘                      └─────────────┘
        │                                    │
        │  MIGRATING state                   │  IMPORTING state
        │                                    │
        ▼                                    ▼
  ┌─────────────────────────────────────────────────────────────────┐
  │                     CLIENT REQUESTS                              │
  ├─────────────────────────────────────────────────────────────────┤
  │                                                                  │
  │  1. Client sends GET key to Source                              │
  │     - If key exists: return value                                │
  │     - If key migrated: return ASK redirect to Target            │
  │                                                                  │
  │  2. Client sends SET key to Source                              │
  │     - If slot migrating: return ASK redirect to Target          │
  │                                                                  │
  │  3. After migration complete:                                    │
  │     - All nodes updated: slot 100 → Target                      │
  │     - Requests get MOVED redirect (permanent)                    │
  │                                                                  │
  └─────────────────────────────────────────────────────────────────┘

  ASK vs MOVED:
  ┌─────────────────────────────────────────────────────────────────┐
  │  ASK:   Temporary redirect during migration                      │
  │         Client should NOT update slot→node mapping              │
  │         Client must send ASKING command before the request      │
  │                                                                  │
  │  MOVED: Permanent redirect (slot ownership changed)              │
  │         Client SHOULD update slot→node mapping cache            │
  │         Next request goes directly to new node                   │
  └─────────────────────────────────────────────────────────────────┘

  go-redis handles all redirects automatically!
`)

	return nil
}
