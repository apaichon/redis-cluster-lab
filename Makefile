# Redis Cluster Scaling Lab - Makefile
# Ticket Reservation System

.PHONY: help build start stop clean init cluster-info visualize set-replica demo scale-up scale-add-replica scale-down failover load-test recover \
        slot-info key-slot hash-tag-demo cross-slot-demo analyze-distribution sharding-demo reshard-demo hotkey-demo migration-demo \
        server k6-smoke k6-load k6-stress k6-concurrent k6-install get-key

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
	@echo "  make visualize     - Visualize cluster architecture (masters & replicas)"
	@echo "  make set-replica REPLICA=7001 MASTER=7005 - Set node as replica of master"
	@echo "  make scale-up      - Add new master node (redis-7)"
	@echo "  make scale-add-replica - Add replica (redis-8) to redis-7"
	@echo "  make scale-down    - Remove node (default: redis-7)"
	@echo "  make failover      - Test automatic failover"
	@echo "  make recover       - Recover failed node (redis-1)"
	@echo ""
	@echo "Application:"
	@echo "  make demo          - Run full demonstration"
	@echo "  make load-test     - Run concurrent load test"
	@echo "  make get-key KEY_NAME=mykey - Get data by key from Redis"
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
	@echo "k6 Load Testing:"
	@echo "  make server        - Start HTTP API server for load testing"
	@echo "  make k6-install    - Install k6 load testing tool"
	@echo "  make k6-smoke      - Run smoke test (verify basic functionality)"
	@echo "  make k6-load       - Run load test (sustained traffic)"
	@echo "  make k6-stress     - Run stress test (find breaking point)"
	@echo "  make k6-concurrent - Run concurrent booking test (race conditions)"
	@echo ""
	@echo "Examples:"
	@echo "  make set-replica REPLICA=7001 MASTER=7005 - Make redis-1 replica of redis-5"
	@echo "  make scale-down NODE=7008  - Remove specific node"
	@echo "  make failover MASTER=7002  - Failover specific master"
	@echo "  make key-slot KEY=user:123 - Check slot for key"
	@echo "  make k6-load VUS=100 DURATION=5m - Load test with 100 users for 5m"
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

# Visualize cluster architecture
visualize:
	@chmod +x scripts/visualize-cluster.sh
	@./scripts/visualize-cluster.sh

# Set a node as replica of a master
REPLICA ?=
MASTER ?=
set-replica:
	@chmod +x scripts/set-replica.sh
	@if [ -z "$(REPLICA)" ] || [ -z "$(MASTER)" ]; then \
		echo "Usage: make set-replica REPLICA=<port> MASTER=<port>"; \
		echo ""; \
		echo "Example: make set-replica REPLICA=7001 MASTER=7005"; \
		echo "         (Makes redis-1:7001 a replica of redis-5:7005)"; \
		echo ""; \
		echo "Current masters:"; \
		docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null | grep master || \
		docker exec redis-2 redis-cli -p 7002 cluster nodes | grep master; \
	else \
		./scripts/set-replica.sh $(REPLICA) $(MASTER); \
	fi

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

# Get data by key
KEY_NAME ?= ""
get-key: build
	@if [ -z "$(KEY_NAME)" ]; then \
		echo "Usage: make get-key KEY_NAME=<key>"; \
	else \
		cd app && ./ticket-reservation get-key $(KEY_NAME); \
	fi

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

# ============================================
# K6 LOAD TESTING
# ============================================

# Server configuration
SERVER_ADDR ?= :8080
SERVER_TTL ?= 15m

# k6 configuration
VUS ?= 10
DURATION ?= 2m
BASE_URL ?= http://localhost:8080

# Install k6 (macOS)
k6-install:
	@echo "Installing k6..."
	@if command -v brew &> /dev/null; then \
		brew install k6; \
	elif command -v apt-get &> /dev/null; then \
		sudo gpg -k; \
		sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69; \
		echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list; \
		sudo apt-get update; \
		sudo apt-get install k6; \
	else \
		echo "Please install k6 manually: https://k6.io/docs/get-started/installation/"; \
	fi
	@echo "k6 installed successfully!"

# Start API server for load testing
server: build
	@echo "Starting API server on $(SERVER_ADDR)..."
	cd app && ./ticket-reservation server --addr $(SERVER_ADDR) --ttl $(SERVER_TTL)

# Start API server in background
server-bg: build
	@echo "Starting API server in background on $(SERVER_ADDR)..."
	@cd app && nohup ./ticket-reservation server --addr $(SERVER_ADDR) --ttl $(SERVER_TTL) > ../server.log 2>&1 &
	@sleep 2
	@echo "Server started. Logs: server.log"
	@echo "To stop: make server-stop"

# Stop background server
server-stop:
	@echo "Stopping API server..."
	@pkill -f "ticket-reservation server" || echo "Server not running"

# k6 smoke test - verify basic functionality
k6-smoke:
	@echo "Running k6 smoke test..."
	@if ! command -v k6 &> /dev/null; then \
		echo "k6 not found. Run 'make k6-install' first."; \
		exit 1; \
	fi
	k6 run --env BASE_URL=$(BASE_URL) loadtest/smoke-test.js

# k6 load test - sustained traffic
k6-load:
	@echo "Running k6 load test with $(VUS) VUs for $(DURATION)..."
	@if ! command -v k6 &> /dev/null; then \
		echo "k6 not found. Run 'make k6-install' first."; \
		exit 1; \
	fi
	k6 run --env BASE_URL=$(BASE_URL) --env VUS=$(VUS) --env DURATION=$(DURATION) loadtest/reservation-test.js

# k6 stress test - find breaking point
k6-stress:
	@echo "Running k6 stress test..."
	@if ! command -v k6 &> /dev/null; then \
		echo "k6 not found. Run 'make k6-install' first."; \
		exit 1; \
	fi
	k6 run --env BASE_URL=$(BASE_URL) loadtest/stress-test.js

# k6 concurrent booking test - test race conditions
k6-concurrent:
	@echo "Running k6 concurrent booking test with $(VUS) VUs..."
	@if ! command -v k6 &> /dev/null; then \
		echo "k6 not found. Run 'make k6-install' first."; \
		exit 1; \
	fi
	k6 run --env BASE_URL=$(BASE_URL) --env VUS=$(VUS) loadtest/concurrent-booking-test.js

# k6 with HTML report
k6-report:
	@echo "Running k6 load test with HTML report..."
	@if ! command -v k6 &> /dev/null; then \
		echo "k6 not found. Run 'make k6-install' first."; \
		exit 1; \
	fi
	k6 run --env BASE_URL=$(BASE_URL) --env VUS=$(VUS) --env DURATION=$(DURATION) \
		--out json=loadtest/results.json loadtest/reservation-test.js
	@echo "Results saved to loadtest/results.json"

# Full load test workflow
k6-full: build
	@echo "Running full k6 load test workflow..."
	@echo ""
	@echo "1. Starting API server in background..."
	@cd app && nohup ./ticket-reservation server --addr $(SERVER_ADDR) --ttl 5m > ../server.log 2>&1 &
	@sleep 3
	@echo "2. Running smoke test..."
	@k6 run --env BASE_URL=$(BASE_URL) loadtest/smoke-test.js || (make server-stop && exit 1)
	@echo ""
	@echo "3. Running load test..."
	@k6 run --env BASE_URL=$(BASE_URL) --env VUS=$(VUS) --env DURATION=$(DURATION) loadtest/reservation-test.js
	@echo ""
	@echo "4. Stopping server..."
	@make server-stop
	@echo ""
	@echo "Full load test complete!"
