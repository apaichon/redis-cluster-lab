# Redis Cluster Scaling Lab - Makefile
# Ticket Reservation System

.PHONY: help build start stop clean init cluster-info demo scale-up scale-add-replica scale-down failover load-test recover \
        slot-info key-slot hash-tag-demo cross-slot-demo analyze-distribution sharding-demo reshard-demo hotkey-demo migration-demo

# Default target
help:
	@echo ""
	@echo "Redis Cluster Scaling Lab - Commands"
	@echo "====================================="
	@echo ""
	@echo "Setup & Management:"
	@echo "  make build         - Build the Go application"
	@echo "  make start         - Start Redis cluster and initialize"
	@echo "  make stop          - Stop all containers"
	@echo "  make clean         - Remove all containers and volumes"
	@echo "  make init          - Initialize the cluster (after start)"
	@echo ""
	@echo "Cluster Operations:"
	@echo "  make cluster-info  - Show cluster status"
	@echo "  make scale-up      - Add new master node (redis-7)"
	@echo "  make scale-add-replica - Add replica (redis-8) to redis-7"
	@echo "  make scale-down    - Remove node (default: redis-7)"
	@echo "  make failover      - Test automatic failover"
	@echo "  make recover       - Recover failed node (redis-1)"
	@echo ""
	@echo "Application:"
	@echo "  make demo          - Run full demonstration"
	@echo "  make load-test     - Run concurrent load test"
	@echo ""
	@echo "Sharding Labs:"
	@echo "  make slot-info     - Show slot distribution across nodes"
	@echo "  make key-slot KEY=mykey - Show which slot/node a key maps to"
	@echo "  make hash-tag-demo - Demonstrate hash tags for co-location"
	@echo "  make cross-slot-demo - Demonstrate cross-slot limitations"
	@echo "  make sharding-demo - Comprehensive sharding demonstration"
	@echo "  make analyze-distribution - Analyze key distribution"
	@echo "  make reshard-demo  - Learn about resharding process"
	@echo "  make hotkey-demo   - Simulate and learn about hot keys"
	@echo "  make migration-demo - Explain key migration"
	@echo ""
	@echo "Examples:"
	@echo "  make scale-down NODE=7008  - Remove specific node"
	@echo "  make failover MASTER=7002  - Failover specific master"
	@echo "  make key-slot KEY=user:123 - Check slot for key"
	@echo ""

# Build the Go application
build:
	@echo "Building ticket-reservation application..."
	cd app && go mod tidy && go build -o ticket-reservation .
	@echo "Build complete: app/ticket-reservation"

# Start Redis cluster
start:
	@echo "Starting Redis cluster (6 nodes)..."
	docker compose up -d
	@sleep 3
	@echo "Initializing cluster..."
	@chmod +x scripts/*.sh
	@./scripts/init-cluster.sh
	@echo ""
	@echo "Cluster is ready! Run 'make cluster-info' to verify."

# Stop all containers
stop:
	@echo "Stopping all containers..."
	docker compose --profile scale down

# Clean up everything
clean:
	@echo "Removing all containers and volumes..."
	docker compose --profile scale down -v
	@echo "Cleanup complete!"

# Initialize cluster (manual)
init:
	@chmod +x scripts/*.sh
	./scripts/init-cluster.sh

# Show cluster status
cluster-info:
	@echo ""
	@echo "Cluster Nodes:"
	@docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null || docker exec redis-2 redis-cli -p 7002 cluster nodes
	@echo ""
	@echo "Cluster Info:"
	@docker exec redis-1 redis-cli -p 7001 cluster info 2>/dev/null | grep -E "cluster_state|cluster_slots|cluster_known_nodes|cluster_size" || \
	 docker exec redis-2 redis-cli -p 7002 cluster info | grep -E "cluster_state|cluster_slots|cluster_known_nodes|cluster_size"

# Run demo
demo: build
	@echo "Running demonstration..."
	cd app && ./ticket-reservation demo

# Scale up - add new master
scale-up:
	@chmod +x scripts/*.sh
	./scripts/scale-add-master.sh

# Scale up - add replica
scale-add-replica:
	@chmod +x scripts/*.sh
	./scripts/scale-add-replica.sh

# Scale down - remove node
NODE ?= 7007
scale-down:
	@chmod +x scripts/*.sh
	./scripts/scale-remove-node.sh $(NODE)

# Failover test
MASTER ?= 7001
failover:
	@chmod +x scripts/*.sh
	./scripts/failover-test.sh $(MASTER)

# Recover failed node
recover:
	@echo "Recovering redis-1..."
	docker start redis-1
	@sleep 3
	@echo "Node recovered. It should rejoin as a replica."
	@make cluster-info

# Load test
USERS ?= 50
SEATS ?= 2
load-test: build
	cd app && ./ticket-reservation load-test --users $(USERS) --seats $(SEATS)

# Application commands (shortcuts)
create-event: build
	cd app && ./ticket-reservation create-event $(ARGS)

reserve: build
	cd app && ./ticket-reservation reserve $(ARGS)

availability: build
	cd app && ./ticket-reservation availability $(ARGS)

# Start spare nodes for scaling
start-spare:
	@echo "Starting spare nodes for scaling..."
	docker compose --profile scale up -d redis-7 redis-8

# Watch cluster state (works on macOS without watch command)
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

# Logs
logs:
	docker compose logs -f

logs-redis1:
	docker logs -f redis-1

# ============================================
# SHARDING LABS
# ============================================

# Show slot distribution
slot-info: build
	cd app && ./ticket-reservation slot-info

# Show which slot/node a key maps to
KEY ?= test:key
key-slot: build
	cd app && ./ticket-reservation key-slot $(KEY)

# Demonstrate hash tags
hash-tag-demo: build
	cd app && ./ticket-reservation hash-tag-demo

# Demonstrate cross-slot limitations
cross-slot-demo: build
	cd app && ./ticket-reservation cross-slot-demo

# Comprehensive sharding demonstration
sharding-demo: build
	cd app && ./ticket-reservation sharding-demo

# Analyze key distribution
PATTERN ?= *
LIMIT ?= 1000
analyze-distribution: build
	cd app && ./ticket-reservation analyze-distribution --pattern "$(PATTERN)" --limit $(LIMIT)

# Learn about resharding
reshard-demo: build
	cd app && ./ticket-reservation reshard-demo

# Simulate hot keys
DURATION ?= 5
hotkey-demo: build
	cd app && ./ticket-reservation hotkey-demo --duration $(DURATION)

# Explain key migration
migration-demo: build
	cd app && ./ticket-reservation migration-demo

# Manual resharding between nodes
FROM ?= ""
TO ?= ""
SLOTS ?= 500
reshard-slots:
	@if [ -z "$(FROM)" ] || [ -z "$(TO)" ]; then \
		echo "Usage: make reshard-slots FROM=<node-id> TO=<node-id> SLOTS=<count>"; \
		echo ""; \
		echo "Current masters:"; \
		docker exec redis-1 redis-cli -p 7001 cluster nodes | grep master; \
	else \
		docker exec redis-1 redis-cli --cluster reshard redis-1:7001 \
			--cluster-from $(FROM) \
			--cluster-to $(TO) \
			--cluster-slots $(SLOTS) \
			--cluster-yes; \
	fi
