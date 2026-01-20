#!/bin/bash

# Test automatic failover by stopping a master
# The replica should be promoted automatically

set -e

MASTER_PORT=${1:-7001}

echo "========================================="
echo "   FAILOVER TEST: SIMULATING MASTER FAILURE"
echo "========================================="

echo ""
echo "Step 1: Current cluster state"
echo "-----------------------------"
docker exec redis-2 redis-cli -p 7002 cluster nodes

# Get master info
MASTER_INFO=$(docker exec redis-2 redis-cli -p 7002 cluster nodes | grep ":${MASTER_PORT}@" | grep master)
MASTER_ID=$(echo "$MASTER_INFO" | cut -d' ' -f1)

if [ -z "$MASTER_ID" ]; then
    echo "Error: No master found on port $MASTER_PORT"
    exit 1
fi

# Find the replica of this master
REPLICA_INFO=$(docker exec redis-2 redis-cli -p 7002 cluster nodes | grep "$MASTER_ID" | grep slave)
REPLICA_PORT=$(echo "$REPLICA_INFO" | grep -o ":[0-9]*@" | tr -d ':@')

echo ""
echo "Target master:"
echo "  ID: $MASTER_ID"
echo "  Port: $MASTER_PORT"
echo "  Replica port: $REPLICA_PORT"

echo ""
echo "Step 2: Stopping master on port $MASTER_PORT..."
echo "------------------------------------------------"
docker stop redis-1

echo ""
echo "Step 3: Waiting for failover detection (cluster-node-timeout: 5s)..."
sleep 7

echo ""
echo "Step 4: Checking cluster state after failover..."
echo "------------------------------------------------"
docker exec redis-2 redis-cli -p 7002 cluster nodes

echo ""
echo "Step 5: Verifying new master status..."
NEW_MASTER=$(docker exec redis-2 redis-cli -p 7002 cluster nodes | grep ":${REPLICA_PORT}@" | grep master)

if [ -n "$NEW_MASTER" ]; then
    echo "SUCCESS: Replica on port $REPLICA_PORT has been promoted to master!"
    echo ""
    echo "New master info:"
    echo "$NEW_MASTER"
else
    echo "WARNING: Failover may not have completed yet. Check manually."
fi

echo ""
echo "Step 6: Cluster info after failover"
echo "-----------------------------------"
docker exec redis-2 redis-cli -p 7002 cluster info | grep -E "cluster_state|cluster_slots|cluster_size"

echo ""
echo "========================================="
echo "   FAILOVER TEST COMPLETE!"
echo "========================================="

echo ""
echo "To recover: docker start redis-1"
echo "The old master will rejoin as a replica of the new master."
echo ""
echo "========================================="
