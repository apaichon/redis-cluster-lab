#!/bin/bash

# Remove a node from the cluster
# If removing a master, slots must be migrated first

set -e

NODE_PORT=${1:-7007}

echo "========================================="
echo "   SCALING DOWN: REMOVE NODE"
echo "========================================="

# Get node info
NODE_INFO=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | grep ":${NODE_PORT}@")
NODE_ID=$(echo "$NODE_INFO" | cut -d' ' -f1)
NODE_TYPE=$(echo "$NODE_INFO" | grep -o "master\|slave")

if [ -z "$NODE_ID" ]; then
    echo "Error: Could not find node on port $NODE_PORT"
    exit 1
fi

echo ""
echo "Node to remove:"
echo "  ID: $NODE_ID"
echo "  Port: $NODE_PORT"
echo "  Type: $NODE_TYPE"

if [ "$NODE_TYPE" == "master" ]; then
    # Check if master has slots
    SLOTS=$(echo "$NODE_INFO" | grep -o "[0-9]*-[0-9]*" | head -1)

    if [ -n "$SLOTS" ]; then
        echo ""
        echo "Step 1: Master has slots, need to reshard first..."
        echo "Current slots: $SLOTS"
        echo ""
        echo "Resharding slots to other masters..."

        # Get another master to receive the slots
        TARGET_ID=$(docker exec redis-1 redis-cli -p 7001 cluster nodes | grep master | grep -v "$NODE_ID" | head -1 | cut -d' ' -f1)

        # Calculate slot count
        SLOT_START=$(echo "$SLOTS" | cut -d'-' -f1)
        SLOT_END=$(echo "$SLOTS" | cut -d'-' -f2)
        SLOT_COUNT=$((SLOT_END - SLOT_START + 1))

        echo "Moving $SLOT_COUNT slots to node $TARGET_ID"

        docker exec redis-1 redis-cli --cluster reshard \
            172.30.0.11:7001 \
            --cluster-from $NODE_ID \
            --cluster-to $TARGET_ID \
            --cluster-slots $SLOT_COUNT \
            --cluster-yes

        echo ""
        echo "Slots resharded successfully!"
    fi
fi

echo ""
echo "Step 2: Removing node from cluster..."
docker exec redis-1 redis-cli --cluster del-node \
    172.30.0.11:7001 \
    $NODE_ID

echo ""
echo "Step 3: Stopping the removed node container..."
# Extract container number from port
CONTAINER_NUM=$((NODE_PORT - 7000))
docker stop redis-${CONTAINER_NUM} 2>/dev/null || true

echo ""
echo "========================================="
echo "   NODE REMOVED SUCCESSFULLY!"
echo "========================================="

echo ""
echo "Updated cluster state:"
docker exec redis-1 redis-cli -p 7001 cluster nodes

echo ""
echo "========================================="
