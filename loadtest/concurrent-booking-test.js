import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter, Trend } from 'k6/metrics';
import { CONFIG, generateUserId } from './config.js';

// Custom metrics for race condition testing
const doubleBookingAttempts = new Counter('double_booking_attempts');
const doubleBookingPrevented = new Counter('double_booking_prevented');
const raceConditionSuccess = new Rate('race_condition_success');
const bookingLatency = new Trend('booking_latency');

// Test configuration - high concurrency to stress race conditions
export const options = {
    scenarios: {
        // Scenario 1: Many users trying to book the same seats simultaneously
        race_condition_test: {
            executor: 'per-vu-iterations',
            vus: parseInt(__ENV.VUS) || 50,
            iterations: 1,
            maxDuration: '2m',
        },
    },
    thresholds: {
        'double_booking_prevented': ['count>0'],
        'race_condition_success': ['rate==1'], // All race conditions must be handled correctly
    },
};

// All VUs will try to book the SAME seats - testing atomicity
const TARGET_SEATS = ['A1', 'A2', 'A3'];

export function setup() {
    console.log(`Starting concurrent booking test against ${CONFIG.BASE_URL}`);
    console.log(`Testing race condition with ${TARGET_SEATS.length} seats: ${TARGET_SEATS.join(', ')}`);

    // Health check
    const healthRes = http.get(`${CONFIG.BASE_URL}/health`);
    if (healthRes.status !== 200) {
        throw new Error('API server is not healthy');
    }

    // Create a small test event
    const eventPayload = JSON.stringify({
        name: `Race Condition Test ${Date.now()}`,
        venue: 'Concurrency Arena',
        rows: 5,
        seats_per_row: 10,
        price_per_seat: 100.00,
    });

    const eventRes = http.post(`${CONFIG.BASE_URL}/events`, eventPayload, {
        headers: { 'Content-Type': 'application/json' },
    });

    if (eventRes.status !== 201) {
        throw new Error('Failed to create test event');
    }

    const event = JSON.parse(eventRes.body);
    console.log(`Created test event: ${event.id}`);

    return {
        eventId: event.id,
        targetSeats: TARGET_SEATS,
    };
}

export default function (data) {
    const userId = generateUserId();

    doubleBookingAttempts.add(1);

    const reservePayload = JSON.stringify({
        event_id: data.eventId,
        user_id: userId,
        seats: data.targetSeats,
        customer_name: `Race Test User`,
        customer_email: `${userId}@test.com`,
    });

    const startTime = Date.now();
    const res = http.post(`${CONFIG.BASE_URL}/reservations`, reservePayload, {
        headers: { 'Content-Type': 'application/json' },
    });
    const latency = Date.now() - startTime;
    bookingLatency.add(latency);

    // Only ONE VU should succeed (201), all others should get conflict (409)
    const isSuccess = res.status === 201;
    const isConflict = res.status === 409;

    if (isConflict) {
        doubleBookingPrevented.add(1);
    }

    // Race condition is handled correctly if we get either success or conflict
    const correctlyHandled = isSuccess || isConflict;
    raceConditionSuccess.add(correctlyHandled);

    check(res, {
        'response is success or conflict': (r) => r.status === 201 || r.status === 409,
        'no server error': (r) => r.status < 500,
    });

    if (isSuccess) {
        console.log(`VU ${__VU}: Successfully reserved seats ${data.targetSeats.join(', ')}`);
    } else if (isConflict) {
        // Expected behavior - seats already taken
    } else {
        console.error(`VU ${__VU}: Unexpected status ${res.status}: ${res.body}`);
    }
}

export function teardown(data) {
    console.log('\n========== CONCURRENT BOOKING TEST RESULTS ==========');

    // Get final availability
    const statsRes = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/availability`);
    if (statsRes.status === 200) {
        const stats = JSON.parse(statsRes.body);
        console.log(`Final event stats:`);
        console.log(`  Total seats: ${stats.total_seats}`);
        console.log(`  Available: ${stats.available_seats}`);
        console.log(`  Pending: ${stats.pending_seats}`);
        console.log(`  Sold: ${stats.sold_seats}`);

        // Verify only 3 seats were reserved (our target seats)
        const reservedCount = stats.pending_seats + stats.sold_seats;
        if (reservedCount === TARGET_SEATS.length) {
            console.log(`\n SUCCESS: Exactly ${TARGET_SEATS.length} seats were reserved (no double booking)`);
        } else if (reservedCount === 0) {
            console.log(`\n INFO: No seats were reserved (all attempts may have conflicted)`);
        } else {
            console.log(`\n WARNING: ${reservedCount} seats reserved, expected ${TARGET_SEATS.length}`);
        }
    }

    console.log('=====================================================\n');
}
