#!/bin/bash

# Load test script for the ticket reservation system
# Runs concurrent reservations to test cluster performance

set -e

NUM_USERS=${1:-50}
SEATS_PER_USER=${2:-2}
EVENT_ID=${3:-""}

echo "========================================="
echo "   LOAD TEST: CONCURRENT RESERVATIONS"
echo "========================================="
echo ""
echo "Configuration:"
echo "  Users: $NUM_USERS"
echo "  Seats per user: $SEATS_PER_USER"
echo ""

cd "$(dirname "$0")/../app"

if [ -z "$EVENT_ID" ]; then
    echo "Creating new event for load test..."
    ./ticket-reservation load-test --users $NUM_USERS --seats $SEATS_PER_USER
else
    echo "Using existing event: $EVENT_ID"
    ./ticket-reservation load-test --event $EVENT_ID --users $NUM_USERS --seats $SEATS_PER_USER
fi

echo ""
echo "========================================="
