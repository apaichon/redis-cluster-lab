import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import { CONFIG, getRandomSeats, generateUserId } from './config.js';

// Custom metrics
const requestsPerSecond = new Trend('requests_per_second');
const successRate = new Rate('success_rate');
const errorRate = new Rate('error_rate');
const availabilityChecks = new Counter('availability_checks');
const reservationAttempts = new Counter('reservation_attempts');

// Stress test configuration - gradually increase load until breaking point
export const options = {
    scenarios: {
        stress_test: {
            executor: 'ramping-arrival-rate',
            startRate: 10,
            timeUnit: '1s',
            preAllocatedVUs: 50,
            maxVUs: 200,
            stages: [
                { duration: '1m', target: 20 },   // Warm up
                { duration: '2m', target: 50 },   // Normal load
                { duration: '2m', target: 100 },  // High load
                { duration: '2m', target: 150 },  // Very high load
                { duration: '1m', target: 200 },  // Breaking point
                { duration: '2m', target: 50 },   // Recovery
                { duration: '1m', target: 0 },    // Cool down
            ],
        },
    },
    thresholds: {
        'http_req_duration': ['p(95)<1000', 'p(99)<2000'],
        'http_req_failed': ['rate<0.05'],
        'success_rate': ['rate>0.9'],
    },
};

export function setup() {
    console.log(`Starting stress test against ${CONFIG.BASE_URL}`);

    // Health check
    const healthRes = http.get(`${CONFIG.BASE_URL}/health`);
    if (healthRes.status !== 200) {
        throw new Error('API server is not healthy');
    }

    // Create a large test event for stress testing
    const eventPayload = JSON.stringify({
        name: `Stress Test Event ${Date.now()}`,
        venue: 'Stress Test Stadium',
        rows: 50,        // 50 rows
        seats_per_row: 100, // 100 seats per row = 5000 total seats
        price_per_seat: 50.00,
    });

    const eventRes = http.post(`${CONFIG.BASE_URL}/events`, eventPayload, {
        headers: { 'Content-Type': 'application/json' },
    });

    if (eventRes.status !== 201) {
        throw new Error('Failed to create test event');
    }

    const event = JSON.parse(eventRes.body);
    console.log(`Created stress test event: ${event.id} with ${event.total_seats} seats`);

    return {
        eventId: event.id,
        rows: 50,
        seatsPerRow: 100,
    };
}

export default function (data) {
    const userId = generateUserId();

    // Randomly choose between different operations
    const operation = Math.random();

    if (operation < 0.4) {
        // 40% - Check availability (read operation)
        availabilityChecks.add(1);

        const res = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/availability`);

        const success = check(res, {
            'availability check successful': (r) => r.status === 200,
        });

        successRate.add(success);
        errorRate.add(!success);

    } else if (operation < 0.9) {
        // 50% - Make a reservation (write operation)
        reservationAttempts.add(1);

        const seats = getRandomSeats(data.rows, data.seatsPerRow, Math.floor(Math.random() * 4) + 1);

        const reservePayload = JSON.stringify({
            event_id: data.eventId,
            user_id: userId,
            seats: seats,
            customer_name: `Stress User`,
            customer_email: `${userId}@stress.test`,
        });

        const res = http.post(`${CONFIG.BASE_URL}/reservations`, reservePayload, {
            headers: { 'Content-Type': 'application/json' },
        });

        // 201 (created) or 409 (conflict) are both acceptable
        const success = check(res, {
            'reservation handled correctly': (r) => r.status === 201 || r.status === 409,
        });

        successRate.add(success);
        errorRate.add(!success);

        // If reservation succeeded, maybe confirm it
        if (res.status === 201 && Math.random() < 0.5) {
            const body = JSON.parse(res.body);
            const confirmRes = http.post(
                `${CONFIG.BASE_URL}/reservations/${body.id}/confirm`,
                JSON.stringify({ payment_id: `pay_stress_${Date.now()}` }),
                { headers: { 'Content-Type': 'application/json' } }
            );

            check(confirmRes, {
                'confirmation handled': (r) => r.status === 200 || r.status === 404,
            });
        }

    } else {
        // 10% - Health check
        const res = http.get(`${CONFIG.BASE_URL}/health`);

        const success = check(res, {
            'health check passed': (r) => r.status === 200,
        });

        successRate.add(success);
        errorRate.add(!success);
    }

    // Very short sleep to maximize throughput
    sleep(Math.random() * 0.1);
}

export function teardown(data) {
    console.log('\n========== STRESS TEST RESULTS ==========');

    // Get final stats
    const statsRes = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/availability`);
    if (statsRes.status === 200) {
        const stats = JSON.parse(statsRes.body);
        console.log(`Final event stats:`);
        console.log(`  Total seats: ${stats.total_seats}`);
        console.log(`  Available: ${stats.available_seats}`);
        console.log(`  Pending: ${stats.pending_seats}`);
        console.log(`  Sold: ${stats.sold_seats}`);
        console.log(`  Revenue: $${stats.revenue.toFixed(2)}`);
    }

    // Get cluster info
    const clusterRes = http.get(`${CONFIG.BASE_URL}/cluster/info`);
    if (clusterRes.status === 200) {
        const cluster = JSON.parse(clusterRes.body);
        console.log(`\nCluster status: ${cluster.state}`);
        console.log(`  Nodes: ${cluster.known_nodes}`);
        console.log(`  Slots OK: ${cluster.slots_ok}/16384`);
    }

    console.log('==========================================\n');
}
