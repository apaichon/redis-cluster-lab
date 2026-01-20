#!/bin/bash

# Initialize Redis Cluster
# Creates a 3-master, 3-replica cluster from nodes 1-6

set -e

echo "========================================="
echo "     REDIS CLUSTER INITIALIZATION"
echo "========================================="

# Wait for all nodes to be ready
echo "Waiting for Redis nodes to be ready..."
for i in 1 2 3 4 5 6; do
    port=$((7000 + i))
    until docker exec redis-$i redis-cli -p $port ping 2>/dev/null | grep -q PONG; do
        echo "Waiting for redis-$i (port $port)..."
        sleep 1
    done
    echo "redis-$i (port $port) is ready!"
done

echo ""
echo "All nodes are ready!"

# Check if cluster is already initialized
CLUSTER_INFO=$(docker exec redis-1 redis-cli -p 7001 cluster info 2>/dev/null || echo "")
if echo "$CLUSTER_INFO" | grep -q "cluster_state:ok"; then
    echo ""
    echo "Cluster is already initialized!"
    echo ""
    docker exec redis-1 redis-cli -p 7001 cluster nodes
    exit 0
fi

echo ""
echo "Creating cluster with 3 masters and 3 replicas..."
echo ""

# Create the cluster using internal Docker network IPs
# redis-1, redis-2, redis-3 will be masters
# redis-4, redis-5, redis-6 will be replicas
docker exec redis-1 redis-cli --cluster create \
    172.30.0.11:7001 \
    172.30.0.12:7002 \
    172.30.0.13:7003 \
    172.30.0.14:7004 \
    172.30.0.15:7005 \
    172.30.0.16:7006 \
    --cluster-replicas 1 \
    --cluster-yes

echo ""
echo "========================================="
echo "     CLUSTER CREATED SUCCESSFULLY!"
echo "========================================="

# Wait for cluster to stabilize
sleep 2

# Show cluster status
echo ""
echo "Cluster Nodes:"
docker exec redis-1 redis-cli -p 7001 cluster nodes

echo ""
echo "Cluster Info:"
docker exec redis-1 redis-cli -p 7001 cluster info | grep -E "cluster_state|cluster_slots|cluster_known_nodes|cluster_size"

echo ""
echo "========================================="
