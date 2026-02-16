#!/bin/bash

# Set a node as replica of a specific master
# Usage: ./set-replica.sh <replica-port> <master-port>

if [ $# -lt 2 ]; then
    echo "Usage: $0 <replica-port> <master-port>"
    echo ""
    echo "Example: $0 7001 7005"
    echo "         (Makes redis-1:7001 a replica of redis-5:7005)"
    echo ""
    echo "Current cluster status:"
    docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null | \
        awk '{print $2, $3}' | column -t || \
        docker exec redis-2 redis-cli -p 7002 cluster nodes | \
        awk '{print $2, $3}' | column -t
    exit 1
fi

REPLICA_PORT=$1
MASTER_PORT=$2

echo "Setting redis node on port $REPLICA_PORT as replica of master on port $MASTER_PORT..."
echo ""

# Get the master node ID
MASTER_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null | \
    grep ":${MASTER_PORT}@" | grep "master" | awk '{print $1}')

if [ -z "$MASTER_ID" ]; then
    # Try from another node
    MASTER_ID=$(docker exec redis-2 redis-cli -p 7002 cluster nodes 2>/dev/null | \
        grep ":${MASTER_PORT}@" | grep "master" | awk '{print $1}')
fi

if [ -z "$MASTER_ID" ]; then
    echo "❌ Error: Could not find master node on port $MASTER_PORT"
    exit 1
fi

echo "✓ Found master node ID: ${MASTER_ID:0:8}..."
echo ""

# Check if replica node exists and is currently a master without slots
REPLICA_INFO=$(docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null | grep ":${REPLICA_PORT}@")

if [ -z "$REPLICA_INFO" ]; then
    REPLICA_INFO=$(docker exec redis-2 redis-cli -p 7002 cluster nodes 2>/dev/null | grep ":${REPLICA_PORT}@")
fi

if [ -z "$REPLICA_INFO" ]; then
    echo "❌ Error: Could not find node on port $REPLICA_PORT"
    exit 1
fi

# Check if it's already a replica
if echo "$REPLICA_INFO" | grep -q "slave"; then
    CURRENT_MASTER=$(echo "$REPLICA_INFO" | awk '{print $4}')
    if [ "$CURRENT_MASTER" = "$MASTER_ID" ]; then
        echo "✓ Node is already a replica of this master"
        exit 0
    else
        echo "⚠️  Node is currently a replica of another master: ${CURRENT_MASTER:0:8}..."
        echo "   Changing replication target..."
    fi
fi

# Check if it's a master with slots - cannot convert directly
if echo "$REPLICA_INFO" | grep -q "master" && echo "$REPLICA_INFO" | grep -qE "[0-9]+-[0-9]+"; then
    echo "❌ Error: Node on port $REPLICA_PORT is a master with assigned slots"
    echo "   You must first move all slots away from this node before converting to replica"
    echo ""
    echo "   Slots assigned: $(echo "$REPLICA_INFO" | awk '{for(i=9;i<=NF;i++) printf "%s ", $i}')"
    exit 1
fi

# Execute the CLUSTER REPLICATE command
echo "Executing: CLUSTER REPLICATE $MASTER_ID on port $REPLICA_PORT..."
docker exec redis-1 redis-cli -p "$REPLICA_PORT" CLUSTER REPLICATE "$MASTER_ID" 2>/dev/null || \
    docker exec "redis-$((REPLICA_PORT - 7000))" redis-cli -p "$REPLICA_PORT" CLUSTER REPLICATE "$MASTER_ID"

if [ $? -eq 0 ]; then
    echo ""
    echo "✅ Success! Node on port $REPLICA_PORT is now a replica of master on port $MASTER_PORT"
    echo ""
    echo "Verifying..."
    sleep 2

    # Show the result
    docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null | grep ":${REPLICA_PORT}@" || \
        docker exec redis-2 redis-cli -p 7002 cluster nodes | grep ":${REPLICA_PORT}@"
else
    echo ""
    echo "❌ Failed to set replica relationship"
    exit 1
fi
