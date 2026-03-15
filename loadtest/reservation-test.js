import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import { CONFIG, getRandomSeats, generateUserId } from './config.js';

// Custom metrics
const reservationSuccessRate = new Rate('reservation_success_rate');
const confirmationSuccessRate = new Rate('confirmation_success_rate');
const reservationDuration = new Trend('reservation_duration');
const confirmationDuration = new Trend('confirmation_duration');
const seatsReserved = new Counter('seats_reserved');
const conflictErrors = new Counter('conflict_errors');

// Test options - can be overridden with environment variables
export const options = {
    scenarios: {
        reservation_flow: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: __ENV.SCENARIO === 'spike'
                ? CONFIG.SCENARIOS.spike.stages
                : [
                    { duration: '30s', target: parseInt(__ENV.VUS) || 10 },
                    { duration: __ENV.DURATION || '2m', target: parseInt(__ENV.VUS) || 10 },
                    { duration: '30s', target: 0 },
                ],
            gracefulRampDown: '30s',
        },
    },
    thresholds: CONFIG.THRESHOLDS,
};

// Shared state - event ID created in setup
export function setup() {
    console.log(`Starting load test against ${CONFIG.BASE_URL}`);

    // Health check
    const healthRes = http.get(`${CONFIG.BASE_URL}/health`);
    if (healthRes.status !== 200) {
        throw new Error('API server is not healthy');
    }

    // Create a test event
    const eventPayload = JSON.stringify({
        name: `Load Test Event ${Date.now()}`,
        venue: 'Load Test Arena',
        rows: CONFIG.EVENT.rows,
        seats_per_row: CONFIG.EVENT.seatsPerRow,
        price_per_seat: CONFIG.EVENT.pricePerSeat,
    });

    const eventRes = http.post(`${CONFIG.BASE_URL}/events`, eventPayload, {
        headers: { 'Content-Type': 'application/json' },
    });

    if (eventRes.status !== 201) {
        console.error(`Failed to create event: ${eventRes.body}`);
        throw new Error('Failed to create test event');
    }

    const event = JSON.parse(eventRes.body);
    console.log(`Created test event: ${event.id} with ${event.total_seats} seats`);

    return {
        eventId: event.id,
        totalSeats: event.total_seats,
        rows: CONFIG.EVENT.rows,
        seatsPerRow: CONFIG.EVENT.seatsPerRow,
    };
}

export default function (data) {
    const userId = generateUserId();
    const seatsToReserve = Math.floor(Math.random() * 3) + 1; // 1-3 seats

    group('Reservation Flow', function () {
        // Step 1: Check availability
        group('Check Availability', function () {
            const availRes = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/availability`);
            check(availRes, {
                'availability status is 200': (r) => r.status === 200,
                'has available seats': (r) => {
                    const body = JSON.parse(r.body);
                    return body.available_seats > 0;
                },
            });
        });

        // Step 2: Get available seats
        let availableSeats = [];
        group('Get Available Seats', function () {
            const seatsRes = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/seats`);
            check(seatsRes, {
                'seats status is 200': (r) => r.status === 200,
            });

            if (seatsRes.status === 200) {
                const body = JSON.parse(seatsRes.body);
                availableSeats = body.available_seats || [];
            }
        });

        // Step 3: Reserve seats
        let reservationId = null;
        group('Reserve Seats', function () {
            // Pick random seats from available
            let seatsToBook;
            if (availableSeats.length >= seatsToReserve) {
                // Shuffle and pick
                const shuffled = availableSeats.sort(() => 0.5 - Math.random());
                seatsToBook = shuffled.slice(0, seatsToReserve);
            } else {
                // Fall back to random generation
                seatsToBook = getRandomSeats(data.rows, data.seatsPerRow, seatsToReserve);
            }

            const reservePayload = JSON.stringify({
                event_id: data.eventId,
                user_id: userId,
                seats: seatsToBook,
                customer_name: `Test User ${userId}`,
                customer_email: `${userId}@test.com`,
            });

            const startTime = Date.now();
            const reserveRes = http.post(`${CONFIG.BASE_URL}/reservations`, reservePayload, {
                headers: { 'Content-Type': 'application/json' },
            });
            const duration = Date.now() - startTime;
            reservationDuration.add(duration);

            const success = reserveRes.status === 201;
            const conflict = reserveRes.status === 409;

            reservationSuccessRate.add(success);

            if (conflict) {
                conflictErrors.add(1);
            }

            check(reserveRes, {
                'reservation created or conflict': (r) => r.status === 201 || r.status === 409,
            });

            if (success) {
                const body = JSON.parse(reserveRes.body);
                reservationId = body.id;
                seatsReserved.add(seatsToBook.length);
            }
        });

        // Step 4: Confirm reservation (if created)
        if (reservationId) {
            sleep(0.5); // Small delay to simulate payment processing

            group('Confirm Reservation', function () {
                const confirmPayload = JSON.stringify({
                    payment_id: `pay_${Date.now()}`,
                });

                const startTime = Date.now();
                const confirmRes = http.post(
                    `${CONFIG.BASE_URL}/reservations/${reservationId}/confirm`,
                    confirmPayload,
                    { headers: { 'Content-Type': 'application/json' } }
                );
                const duration = Date.now() - startTime;
                confirmationDuration.add(duration);

                const success = confirmRes.status === 200;
                confirmationSuccessRate.add(success);

                check(confirmRes, {
                    'confirmation successful': (r) => r.status === 200,
                });
            });
        }
    });

    sleep(Math.random() * 2 + 0.5); // 0.5-2.5s between iterations
}

export function teardown(data) {
    console.log(`Load test completed for event: ${data.eventId}`);

    // Get final stats
    const statsRes = http.get(`${CONFIG.BASE_URL}/events/${data.eventId}/availability`);
    if (statsRes.status === 200) {
        const stats = JSON.parse(statsRes.body);
        console.log(`Final stats:`);
        console.log(`  Total seats: ${stats.total_seats}`);
        console.log(`  Available: ${stats.available_seats}`);
        console.log(`  Pending: ${stats.pending_seats}`);
        console.log(`  Sold: ${stats.sold_seats}`);
        console.log(`  Revenue: $${stats.revenue}`);
    }
}
