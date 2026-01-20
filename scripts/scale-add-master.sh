#!/bin/bash

# Add a new master node to the cluster and rebalance slots
# This script adds redis-7 as a new master

set -e

echo "========================================="
echo "   SCALING UP: ADD NEW MASTER NODE"
echo "========================================="

# Check if redis-7 is running
if ! docker ps | grep -q redis-7; then
    echo "Starting redis-7..."
    docker compose --profile scale up -d redis-7
    sleep 3
fi

# Wait for redis-7 to be ready
echo "Waiting for redis-7 to be ready..."
until docker exec redis-7 redis-cli -p 7007 ping 2>/dev/null | grep -q PONG; do
    sleep 1
done

echo ""
echo "Step 1: Current cluster state"
echo "-----------------------------"
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep master

echo ""
echo "Step 2: Adding redis-7 (172.30.0.17:7007) as new master..."
echo "---------------------------------------------------"
docker exec redis-1 redis-cli --cluster add-node \
    172.30.0.17:7007 \
    172.30.0.11:7001

echo ""
echo "Step 3: Waiting for node to join..."
sleep 2

echo ""
echo "Step 4: Checking new node status..."
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep 7007

echo ""
echo "Step 5: Rebalancing slots to new master..."
echo "------------------------------------------"
echo "This will move ~4096 slots to the new master (16384 / 4 masters)"
docker exec redis-1 redis-cli --cluster rebalance \
    172.30.0.11:7001 \
    --cluster-use-empty-masters

echo ""
echo "========================================="
echo "   NEW MASTER ADDED AND REBALANCED!"
echo "========================================="

echo ""
echo "Updated cluster state:"
docker exec redis-1 redis-cli -p 7001 cluster nodes | grep master

echo ""
echo "========================================="
