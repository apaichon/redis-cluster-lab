# k6 Load Testing for Ticket Reservation System

This directory contains k6 load test scripts for testing the Redis Cluster ticket reservation system.

## Prerequisites

1. **Install k6:**
   ```bash
   # macOS
   brew install k6

   # Or use make command
   make k6-install
   ```

2. **Start Redis Cluster:**
   ```bash
   make start
   ```

3. **Start API Server:**
   ```bash
   # In one terminal
   make server

   # Or run in background
   make server-bg
   ```

## Test Scripts

### 1. Smoke Test (`smoke-test.js`)
Basic functionality verification with minimal load.

```bash
make k6-smoke
```

**What it tests:**
- Health check endpoint
- Cluster info endpoint
- Event creation
- Seat reservation
- Reservation confirmation
- Conflict detection

### 2. Load Test (`reservation-test.js`)
Sustained traffic simulation with configurable VUs and duration.

```bash
# Default (10 VUs, 2 minutes)
make k6-load

# Custom configuration
make k6-load VUS=50 DURATION=5m
```

**What it tests:**
- Full reservation flow (check → reserve → confirm)
- Concurrent user behavior
- System throughput under load

### 3. Stress Test (`stress-test.js`)
Gradually increases load to find system breaking point.

```bash
make k6-stress
```

**Stages:**
1. Warm up: 10 → 20 req/s
2. Normal load: 50 req/s
3. High load: 100 req/s
4. Very high load: 150 req/s
5. Breaking point: 200 req/s
6. Recovery: back to 50 req/s

### 4. Concurrent Booking Test (`concurrent-booking-test.js`)
Tests race condition handling for atomic seat reservations.

```bash
# Default (50 concurrent VUs)
make k6-concurrent

# More concurrent users
make k6-concurrent VUS=100
```

**What it tests:**
- All VUs try to book the SAME seats simultaneously
- Verifies only ONE VU succeeds (others get 409 Conflict)
- Ensures no double booking occurs

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | `http://localhost:8080` | API server URL |
| `VUS` | `10` | Number of virtual users |
| `DURATION` | `2m` | Test duration |

### Custom Thresholds

Default thresholds in `config.js`:
- `http_req_duration`: p(95) < 500ms, p(99) < 1000ms
- `http_req_failed`: rate < 1%
- `reservation_success_rate`: rate > 95%

## Running Tests

### Quick Start

```bash
# Terminal 1: Start everything
make start

# Terminal 2: Start API server
make server

# Terminal 3: Run tests
make k6-smoke      # Verify basics
make k6-load       # Standard load test
make k6-concurrent # Test race conditions
```

### Full Automated Workflow

```bash
make k6-full VUS=50 DURATION=3m
```

This will:
1. Build the application
2. Start API server in background
3. Run smoke test
4. Run load test
5. Stop server

### Generate Report

```bash
make k6-report VUS=50 DURATION=5m
```

Results saved to `loadtest/results.json`

## API Endpoints

The API server exposes:

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/cluster/info` | Redis cluster info |
| POST | `/events` | Create event |
| GET | `/events/{id}` | Get event |
| GET | `/events/{id}/availability` | Get availability stats |
| GET | `/events/{id}/seats` | Get available seats |
| POST | `/reservations` | Create reservation |
| GET | `/reservations/{id}` | Get reservation |
| POST | `/reservations/{id}/confirm` | Confirm reservation |
| POST | `/reservations/{id}/cancel` | Cancel reservation |
| POST | `/waitlist` | Join waitlist |

## Interpreting Results

### Key Metrics

- **http_req_duration**: Response time
  - p(50): Median latency
  - p(95): 95th percentile
  - p(99): 99th percentile

- **http_req_failed**: Request failure rate
  - Should be < 1% under normal conditions

- **reservation_success_rate**: Successful reservations
  - Should be > 95% (accounts for natural conflicts)

- **conflict_errors**: Expected seat conflicts
  - Indicates system correctly handling race conditions

### Example Output

```
     ✓ reservation created or conflict
     ✓ confirmation successful

     checks.....................: 98.5% ✓ 4925 ✗ 75

     http_req_duration..........: avg=45ms min=5ms med=35ms max=500ms p(95)=120ms p(99)=250ms
     http_req_failed............: 0.00% ✓ 0    ✗ 5000

     reservation_success_rate...: 96.5% ✓ 4825 ✗ 175
     confirmation_success_rate..: 99.2% ✓ 4785 ✗ 40

     seats_reserved.............: 9650
     conflict_errors............: 175
```

## Troubleshooting

### Server Connection Refused
```bash
# Check if server is running
curl http://localhost:8080/health

# Start server
make server
```

### Redis Connection Failed
```bash
# Check cluster status
make cluster-info

# Restart cluster
make stop && make start
```

### k6 Not Found
```bash
make k6-install
```
