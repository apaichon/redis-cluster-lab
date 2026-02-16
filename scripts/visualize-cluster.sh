#!/bin/bash

# Visualize Redis Cluster Architecture
# Shows masters with their replicas in a tree structure

echo ""
echo "╔════════════════════════════════════════════════════════════════════╗"
echo "║           REDIS CLUSTER ARCHITECTURE                               ║"
echo "╚════════════════════════════════════════════════════════════════════╝"
echo ""

# Get cluster nodes info
NODES=$(docker exec redis-1 redis-cli -p 7001 cluster nodes 2>/dev/null || \
        docker exec redis-2 redis-cli -p 7002 cluster nodes 2>/dev/null)

if [ -z "$NODES" ]; then
    echo "❌ Cannot connect to cluster"
    exit 1
fi

# Parse and display masters with their replicas
echo "$NODES" | grep "master" | while read -r line; do
    NODE_ID=$(echo "$line" | awk '{print $1}')
    ADDRESS=$(echo "$line" | awk '{print $2}' | cut -d'@' -f1)
    PORT=$(echo "$ADDRESS" | cut -d':' -f2)
    SLOTS=$(echo "$line" | awk '{for(i=9;i<=NF;i++) printf "%s ", $i}')

    # Check if this master has slots (not empty master)
    if [ -n "$SLOTS" ] && [ "$SLOTS" != " " ]; then
        SLOT_RANGE=$(echo "$SLOTS" | sed 's/ /, /g' | sed 's/, $//')
    else
        SLOT_RANGE="(no slots assigned)"
    fi

    echo "🔷 MASTER: redis-$((PORT - 7000)) (port $PORT)"
    echo "   ├─ Node ID: ${NODE_ID:0:8}..."
    echo "   └─ Slots: $SLOT_RANGE"

    # Find replicas for this master
    REPLICAS=$(echo "$NODES" | grep "slave $NODE_ID")

    if [ -n "$REPLICAS" ]; then
        echo "$REPLICAS" | while read -r replica_line; do
            REPLICA_ADDRESS=$(echo "$replica_line" | awk '{print $2}' | cut -d'@' -f1)
            REPLICA_PORT=$(echo "$REPLICA_ADDRESS" | cut -d':' -f2)
            REPLICA_ID=$(echo "$replica_line" | awk '{print $1}')

            echo "      └─ 📦 REPLICA: redis-$((REPLICA_PORT - 7000)) (port $REPLICA_PORT)"
            echo "         └─ Node ID: ${REPLICA_ID:0:8}..."
        done
    else
        echo "      └─ ⚠️  No replicas"
    fi

    echo ""
done

# Show cluster summary
echo "═══════════════════════════════════════════════════════════════════"
echo "CLUSTER SUMMARY"
echo "═══════════════════════════════════════════════════════════════════"

CLUSTER_INFO=$(docker exec redis-1 redis-cli -p 7001 cluster info 2>/dev/null || \
               docker exec redis-2 redis-cli -p 7002 cluster info 2>/dev/null)

STATE=$(echo "$CLUSTER_INFO" | grep "cluster_state" | cut -d':' -f2 | tr -d '\r')
SLOTS=$(echo "$CLUSTER_INFO" | grep "cluster_slots_assigned" | cut -d':' -f2 | tr -d '\r')
NODES_COUNT=$(echo "$CLUSTER_INFO" | grep "cluster_known_nodes" | cut -d':' -f2 | tr -d '\r')
MASTERS=$(echo "$CLUSTER_INFO" | grep "cluster_size" | cut -d':' -f2 | tr -d '\r')

if [ "$STATE" = "ok" ]; then
    STATE_ICON="✅"
else
    STATE_ICON="❌"
fi

echo "State: $STATE_ICON $STATE"
echo "Total Nodes: $NODES_COUNT"
echo "Master Nodes: $MASTERS"
echo "Slots Assigned: $SLOTS / 16384"
echo ""
