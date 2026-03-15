# Part 7: Caching Patterns — Lab Cheatsheet

> Commands-only quick reference. See `Part7-Caching-Patterns-Complete-Guide.md` for full explanations.

---

## 1. Setup

```bash
# Start services
cd /path/to/redis-cluster-lab
docker-compose up -d

# Verify Redis
docker exec redis-1 redis-cli -p 7001 cluster info | grep cluster_state

# Verify PostgreSQL
docker exec postgres pg_isready -U postgres

# Set env
export PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable"

# Build
cd app
go build -o ticket-reservation .

# Create test event (save the event ID!)
./ticket-reservation create-event --name "Caching Lab" --rows 5 --seats 10 --price 100
export EVENT_ID="<paste-event-id>"
```

---

## 2. Cache-Aside (Lazy Loading)

```bash
# Check event in Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$EVENT_ID}"

# Delete key to simulate cold cache
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}"

# Read with --pattern cache-aside — triggers fallback to PostgreSQL
./ticket-reservation availability $EVENT_ID --pattern cache-aside

# Seat map with cache-aside
./ticket-reservation seat-map $EVENT_ID --pattern cache-aside

# Verify key is back in Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$EVENT_ID}"

# Verify in PostgreSQL
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, total_seats FROM events WHERE id = '$EVENT_ID';"
```

**Via API:**
```bash
curl -s "http://localhost:9090/events/$EVENT_ID?pattern=cache-aside" | jq .
curl -s "http://localhost:9090/events/$EVENT_ID/seats?pattern=cache-aside" | jq .
```

---

## 3. Read-Through

```bash
# Delete key
docker exec redis-1 redis-cli -p 7001 -c DEL "{event:$EVENT_ID}"

# get-key only checks Redis — returns nothing
./ticket-reservation get-key "{event:$EVENT_ID}"

# Read-through — cache auto-loads from PG (transparent to caller)
./ticket-reservation availability $EVENT_ID --pattern read-through

# Key is back
docker exec redis-1 redis-cli -p 7001 -c EXISTS "{event:$EVENT_ID}"
```

**Via API:**
```bash
curl -s "http://localhost:9090/events/$EVENT_ID?pattern=read-through" | jq .
curl -s "http://localhost:9090/events/$EVENT_ID/availability?pattern=read-through" | jq .
```

---

## 4. Refresh-Ahead

```bash
# Set key with short TTL (30s)
docker exec redis-1 redis-cli -p 7001 -c SET "demo:refresh" "original" EX 30

# Watch TTL decay
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"
sleep 10
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"

# Wait for refresh window (70% elapsed = 21s)
sleep 12
docker exec redis-1 redis-cli -p 7001 -c TTL "demo:refresh"
# ~8s remaining — refresh-ahead would trigger here

# Simulate refresh (reset TTL with new data)
docker exec redis-1 redis-cli -p 7001 -c SET "demo:refresh" "refreshed" EX 30
docker exec redis-1 redis-cli -p 7001 -c GET "demo:refresh"

# Cleanup
docker exec redis-1 redis-cli -p 7001 -c DEL "demo:refresh"
```

**Via CLI:**
```bash
./ticket-reservation availability $EVENT_ID --pattern refresh-ahead
```

**Via API:**
```bash
curl -s "http://localhost:9090/events/$EVENT_ID?pattern=refresh-ahead" | jq .
```

---

## 5. Write-Through

```bash
# Create event — writes to both Redis and PostgreSQL
./ticket-reservation create-event --name "Write-Through Test" --rows 3 --seats 5 --price 75
export WT_EVENT="<event-id>"

# Verify Redis
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$WT_EVENT}"
docker exec redis-1 redis-cli -p 7001 -c HGETALL "{event:$WT_EVENT}:seats"

# Verify PostgreSQL
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, total_seats FROM events WHERE id = '$WT_EVENT';"
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status FROM seats WHERE event_id = '$WT_EVENT' ORDER BY seat_id LIMIT 10;"

# Reserve seats
./ticket-reservation reserve --event $WT_EVENT --user user1 --seats A1,A2 --name "John" --email "john@test.com"
export RES_ID="<reservation-id>"

# Check both stores — seats should be "pending"
docker exec redis-1 redis-cli -p 7001 -c HMGET "{event:$WT_EVENT}:seats" A1 A2
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status, held_by FROM seats WHERE event_id = '$WT_EVENT' AND seat_id IN ('A1','A2');"

# Confirm reservation
./ticket-reservation confirm $RES_ID --payment pay_001

# Check both stores — seats should be "sold"
docker exec redis-1 redis-cli -p 7001 -c HMGET "{event:$WT_EVENT}:seats" A1 A2
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status, sold_to FROM seats WHERE event_id = '$WT_EVENT' AND seat_id IN ('A1','A2');"
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, status, payment_id FROM reservations WHERE id = '$RES_ID';"
```

---

## 6. Write-Behind (Async)

```bash
# Write to Redis immediately
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$EVENT_ID}:seats" "E1" "pending"

# Queue to Redis Stream
docker exec redis-1 redis-cli -p 7001 -c XADD "write_behind:seat_updates" "*" \
  event_id "$EVENT_ID" seat_id "E1" status "pending" user_id "demo_user"

# Redis has data (fast!)
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:$EVENT_ID}:seats" E1

# PostgreSQL does NOT have it yet (async!)
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT seat_id, status FROM seats WHERE event_id = '$EVENT_ID' AND seat_id = 'E1';"

# Read the stream (simulating consumer)
docker exec redis-1 redis-cli -p 7001 -c XRANGE "write_behind:seat_updates" - + COUNT 5

# Cleanup
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$EVENT_ID}:seats" "E1" "available"
docker exec redis-1 redis-cli -p 7001 -c DEL "write_behind:seat_updates"
```

---

## 7. Write-Around

```bash
# Via CLI — writes only to PostgreSQL, skips Redis cache
./ticket-reservation create-event --name "Write-Around Event" --rows 4 --seats 5 --price 25 --pattern write-around
export WA_EVENT="<event-id>"

# Redis has nothing
docker exec redis-1 redis-cli -p 7001 -c GET "{event:$WA_EVENT}"

# PostgreSQL has data
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "SELECT id, name, total_seats FROM events WHERE id = '$WA_EVENT';"

# First read via app triggers cache population (cache-aside)
./ticket-reservation availability $WA_EVENT --pattern cache-aside
```

**Via raw SQL (alternative):**
```bash
docker exec postgres psql -U postgres -d ticket_reservation -c \
  "INSERT INTO events (id, name, venue, event_date, total_seats, rows, seats_per_row, price_per_seat, created_at)
   VALUES ('wa-test-01', 'Write-Around SQL', 'Test Venue', NOW() + INTERVAL '30 days', 20, 4, 5, 25.00, NOW())
   ON CONFLICT DO NOTHING;"
```

**Via API:**
```bash
curl -s -X POST "http://localhost:9090/events?pattern=write-around" \
  -H "Content-Type: application/json" \
  -d '{"name":"Write-Around API","rows":2,"seats_per_row":4}' | jq .
```

---

## 8. Reconciliation

```bash
# Create event + reserve + confirm
./ticket-reservation create-event --name "Reconcile Test" --rows 2 --seats 5 --price 50
export RECON_EVENT="<event-id>"

./ticket-reservation reserve --event $RECON_EVENT --user user1 --seats A1,A2
export RECON_RES="<reservation-id>"

./ticket-reservation confirm $RECON_RES --payment pay_recon

# Break Redis — simulate drift
docker exec redis-1 redis-cli -p 7001 -c HSET "{event:$RECON_EVENT}:seats" A1 "available"

# Run reconciliation — fixes mismatch
./ticket-reservation reconcile $RECON_EVENT

# Verify fix
docker exec redis-1 redis-cli -p 7001 -c HGET "{event:$RECON_EVENT}:seats" A1
# Expected: "sold"
```

---

## 9. Full Demo (pg-demo)

```bash
PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable" \
  ./ticket-reservation pg-demo
```

---

## 10. API Server

```bash
# Start server (3 ways to pass PostgreSQL DSN)

# Option 1: via env var
export PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable"
./ticket-reservation server

# Option 2: via --pg-dsn flag
./ticket-reservation server --pg-dsn "postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable"

# Option 3: inline env var
PG_DSN="postgres://postgres:postgres@localhost:5533/ticket_reservation?sslmode=disable" \
  ./ticket-reservation server

# Server flags:
#   --addr <addr>       Server address (default: :8080)
#   --ttl <duration>    Reservation TTL (default: 15m)
#   --pg-dsn <dsn>      PostgreSQL DSN (or set PG_DSN env var)

# Custom port example
./ticket-reservation server --addr :9090 --ttl 10m

# --- In another terminal ---

# Create event (Write-Through)
curl -s -X POST http://localhost:9090/events \
  -H "Content-Type: application/json" \
  -d '{"name":"API Test","rows":3,"seats_per_row":5}' | jq .
export API_EVENT="<event-id>"

# Get event — different patterns
curl -s "http://localhost:9090/events/$API_EVENT" | jq .
curl -s "http://localhost:9090/events/$API_EVENT?pattern=cache-aside" | jq .
curl -s "http://localhost:9090/events/$API_EVENT?pattern=read-through" | jq .
curl -s "http://localhost:9090/events/$API_EVENT?pattern=refresh-ahead" | jq .

# Get availability
curl -s "http://localhost:9090/events/$API_EVENT/availability" | jq .
curl -s "http://localhost:9090/events/$API_EVENT/availability?pattern=read-through" | jq .

# Get seats
curl -s "http://localhost:9090/events/$API_EVENT/seats" | jq .
curl -s "http://localhost:9090/events/$API_EVENT/seats?pattern=cache-aside" | jq .

# Reserve
curl -s -X POST http://localhost:9090/reservations \
  -H "Content-Type: application/json" \
  -d "{\"event_id\":\"$API_EVENT\",\"user_id\":\"u1\",\"seats\":[\"A1\",\"A2\"],\"customer_name\":\"Test\"}" | jq .
export API_RES="<reservation-id>"

# Confirm
curl -s -X POST "http://localhost:9090/reservations/$API_RES/confirm" \
  -H "Content-Type: application/json" \
  -d '{"payment_id":"pay_api_001"}' | jq .

# Reconcile
curl -s "http://localhost:9090/reconcile?event_id=$API_EVENT" | jq .
```

---

## 11. Cleanup

```bash
cd /path/to/redis-cluster-lab
docker-compose down        # stop services
docker-compose down -v     # stop + remove volumes
```

---

## Quick Reference

| Pattern | API `?pattern=` | CLI `--pattern` | Commands |
|---------|----------------|-----------------|----------|
| Cache-Aside | `cache-aside` | `--pattern cache-aside` | `availability`, `seat-map` |
| Read-Through | `read-through` | `--pattern read-through` | `availability` |
| Refresh-Ahead | `refresh-ahead` | `--pattern refresh-ahead` | `availability` |
| Write-Through | _(default)_ | _(default)_ | `create-event`, `reserve`, `confirm`, `cancel` |
| Write-Behind | — | — | `redis-cli XADD` |
| Write-Around | `write-around` | `--pattern write-around` | `create-event` |
| Reconciliation | `/reconcile` | — | `reconcile <event-id>` |

**Start server:**
```bash
./ticket-reservation server [--addr :8080] [--ttl 15m] [--pg-dsn "..."]
```
