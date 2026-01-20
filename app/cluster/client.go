package cluster

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"ticket-reservation/models"

	"github.com/redis/go-redis/v9"
)

// addressMapper maps internal Docker IPs to localhost for host access
var addressMapper = map[string]string{
	"172.30.0.11": "127.0.0.1",
	"172.30.0.12": "127.0.0.1",
	"172.30.0.13": "127.0.0.1",
	"172.30.0.14": "127.0.0.1",
	"172.30.0.15": "127.0.0.1",
	"172.30.0.16": "127.0.0.1",
	"172.30.0.17": "127.0.0.1",
	"172.30.0.18": "127.0.0.1",
}

// remapAddress converts internal Docker IP:port to localhost:port
func remapAddress(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if mapped, ok := addressMapper[host]; ok {
		return net.JoinHostPort(mapped, port)
	}
	return addr
}

// Client wraps the Redis cluster client with additional functionality
type Client struct {
	rdb *redis.ClusterClient
	ctx context.Context
}

// DefaultClusterAddrs returns the default cluster node addresses
func DefaultClusterAddrs() []string {
	return []string{
		"127.0.0.1:7001",
		"127.0.0.1:7002",
		"127.0.0.1:7003",
		"127.0.0.1:7004",
		"127.0.0.1:7005",
		"127.0.0.1:7006",
	}
}

// NewClient creates a new Redis cluster client
func NewClient(addrs []string) (*Client, error) {
	if len(addrs) == 0 {
		addrs = DefaultClusterAddrs()
	}

	rdb := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:           addrs,
		MaxRetries:      5,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: 500 * time.Millisecond,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        10,
		MinIdleConns:    5,
		// Route read commands to replicas for better distribution
		RouteRandomly: true,
		// Custom dialer to remap Docker internal IPs to localhost
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			mappedAddr := remapAddress(addr)
			netDialer := &net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Minute,
			}
			return netDialer.DialContext(ctx, network, mappedAddr)
		},
	})

	ctx := context.Background()

	// Test connection with retries
	var err error
	for i := 0; i < 5; i++ {
		err = rdb.Ping(ctx).Err()
		if err == nil {
			break
		}
		time.Sleep(time.Second * time.Duration(i+1))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis cluster: %w", err)
	}

	return &Client{
		rdb: rdb,
		ctx: ctx,
	}, nil
}

// Close closes the Redis cluster connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Redis returns the underlying Redis cluster client
func (c *Client) Redis() *redis.ClusterClient {
	return c.rdb
}

// Context returns the context
func (c *Client) Context() context.Context {
	return c.ctx
}

// Ping tests the cluster connection
func (c *Client) Ping() error {
	return c.rdb.Ping(c.ctx).Err()
}

// GetClusterInfo retrieves cluster state information
func (c *Client) GetClusterInfo() (*models.ClusterInfo, error) {
	info, err := c.rdb.ClusterInfo(c.ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster info: %w", err)
	}

	clusterInfo := &models.ClusterInfo{}

	// Parse cluster info
	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]

		switch key {
		case "cluster_state":
			clusterInfo.State = value
		case "cluster_slots_assigned":
			clusterInfo.SlotsAssigned, _ = strconv.Atoi(value)
		case "cluster_slots_ok":
			clusterInfo.SlotsOK, _ = strconv.Atoi(value)
		case "cluster_slots_pfail":
			clusterInfo.SlotsPFail, _ = strconv.Atoi(value)
		case "cluster_slots_fail":
			clusterInfo.SlotsFail, _ = strconv.Atoi(value)
		case "cluster_known_nodes":
			clusterInfo.KnownNodes, _ = strconv.Atoi(value)
		case "cluster_size":
			clusterInfo.Size, _ = strconv.Atoi(value)
		}
	}

	// Get node information
	nodes, err := c.GetClusterNodes()
	if err == nil {
		clusterInfo.Nodes = nodes
	}

	return clusterInfo, nil
}

// GetClusterNodes retrieves information about all cluster nodes
func (c *Client) GetClusterNodes() ([]models.ClusterNode, error) {
	nodesStr, err := c.rdb.ClusterNodes(c.ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	var nodes []models.ClusterNode
	lines := strings.Split(nodesStr, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		node := models.ClusterNode{
			ID:      parts[0],
			Address: strings.Split(parts[1], "@")[0],
		}

		// Parse flags
		flags := parts[2]
		if strings.Contains(flags, "master") {
			node.Role = "master"
		} else if strings.Contains(flags, "slave") {
			node.Role = "replica"
		}

		// Master ID (for replicas)
		if parts[3] != "-" {
			node.MasterID = parts[3]
		}

		// Slots (for masters)
		if len(parts) > 8 && node.Role == "master" {
			node.Slots = strings.Join(parts[8:], " ")
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// GetSlotForKey returns the hash slot for a given key
func (c *Client) GetSlotForKey(key string) int {
	return int(c.rdb.ClusterKeySlot(c.ctx, key).Val())
}

// GetNodeForSlot returns which node handles a specific slot
func (c *Client) GetNodeForSlot(slot int) (string, error) {
	nodes, err := c.GetClusterNodes()
	if err != nil {
		return "", err
	}

	for _, node := range nodes {
		if node.Role != "master" || node.Slots == "" {
			continue
		}

		// Parse slot ranges
		ranges := strings.Fields(node.Slots)
		for _, r := range ranges {
			if strings.Contains(r, "-") {
				parts := strings.Split(r, "-")
				if len(parts) == 2 {
					start, _ := strconv.Atoi(parts[0])
					end, _ := strconv.Atoi(parts[1])
					if slot >= start && slot <= end {
						return node.Address, nil
					}
				}
			} else {
				s, _ := strconv.Atoi(r)
				if s == slot {
					return node.Address, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no node found for slot %d", slot)
}

// PrintClusterStatus prints a formatted cluster status
func (c *Client) PrintClusterStatus() error {
	info, err := c.GetClusterInfo()
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("         REDIS CLUSTER STATUS")
	fmt.Println("========================================")
	fmt.Printf("State:          %s\n", info.State)
	fmt.Printf("Cluster Size:   %d masters\n", info.Size)
	fmt.Printf("Known Nodes:    %d\n", info.KnownNodes)
	fmt.Printf("Slots Assigned: %d/16384\n", info.SlotsAssigned)
	fmt.Printf("Slots OK:       %d\n", info.SlotsOK)
	fmt.Printf("Slots PFail:    %d\n", info.SlotsPFail)
	fmt.Printf("Slots Fail:     %d\n", info.SlotsFail)

	fmt.Println("\n--- Nodes ---")
	for _, node := range info.Nodes {
		role := node.Role
		if role == "replica" {
			fmt.Printf("  [REPLICA] %s -> master %s\n", node.Address, node.MasterID[:8])
		} else {
			fmt.Printf("  [MASTER]  %s slots: %s\n", node.Address, node.Slots)
		}
	}
	fmt.Println("========================================\n")

	return nil
}

// ForEachMaster executes a function on each master node
func (c *Client) ForEachMaster(fn func(client *redis.Client) error) error {
	return c.rdb.ForEachMaster(c.ctx, func(ctx context.Context, client *redis.Client) error {
		return fn(client)
	})
}

// ForEachShard executes a function on each shard (master + replicas)
func (c *Client) ForEachShard(fn func(client *redis.Client) error) error {
	return c.rdb.ForEachShard(c.ctx, func(ctx context.Context, client *redis.Client) error {
		return fn(client)
	})
}
