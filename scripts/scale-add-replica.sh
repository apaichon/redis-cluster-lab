#!/bin/bash

# Add a replica to an existing master
# By default, adds redis-8 as replica of redis-7

set -e

MASTER_PORT=${1:-7007}

echo "========================================="
echo "   SCALING UP: ADD REPLICA NODE"
echo "========================================="

# Check if redis-8 is running
if ! docker ps | grep -q redis-8; then
    echo "Starting redis-8..."
    docker compose --profile scale up -d redis-8
    sleep 3
fi

# Wait for redis-8 to be ready
echo "Waiting for redis-8 to be ready..."
until docker exec redis-8 redis-cli -p 7008 ping 2>/dev/null | grep -q PONG; do
    sleep 1
done

# Get master node ID
MASTER_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":${MASTER_PORT}@" | grep master | cut -d' ' -f1)

if [ -z "$MASTER_ID" ]; then
    echo "Error: Could not find master on port $MASTER_PORT"
    exit 1
fi

echo ""
echo "Step 1: Target master node"
echo "--------------------------"
echo "Master ID: $MASTER_ID"
echo "Master Port: $MASTER_PORT"

echo ""
echo "Step 2: Adding redis-8 (172.30.0.18:7008) as replica..."
echo "-------------------------------------------------"
docker exec redis-1 redis-cli --cluster add-node \
    172.30.0.18:7008 \
    172.30.0.11:7001 \
    --cluster-slave \
    --cluster-master-id $MASTER_ID

echo ""
echo "Step 3: Waiting for replication to start..."
sleep 3

echo ""
echo "========================================="
echo "   REPLICA ADDED SUCCESSFULLY!"
echo "========================================="

echo ""
echo "Updated cluster state (showing master and its replicas):"
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep -E "(${MASTER_ID}|7008)"

echo ""
echo "Replication status for new replica:"
docker exec redis-8 redis-cli -p 7008 info replication | grep -E "role|master_host|master_port|master_link_status"

echo ""
echo "========================================="
