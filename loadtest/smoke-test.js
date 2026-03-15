import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { CONFIG } from './config.js';

// Smoke test - verify basic functionality works
export const options = {
    vus: 1,
    duration: '30s',
    thresholds: {
        'http_req_failed': ['rate==0'],
        'http_req_duration': ['p(95)<1000'],
    },
};

export function setup() {
    console.log(`Running smoke test against ${CONFIG.BASE_URL}`);
    return {};
}

export default function () {
    let eventId = null;
    let reservationId = null;

    group('Health Check', function () {
        const res = http.get(`${CONFIG.BASE_URL}/health`);
        check(res, {
            'health check returns 200': (r) => r.status === 200,
            'health check returns ok status': (r) => {
                const body = JSON.parse(r.body);
                return body.status === 'ok';
            },
        });
    });

    group('Cluster Info', function () {
        const res = http.get(`${CONFIG.BASE_URL}/cluster/info`);
        check(res, {
            'cluster info returns 200': (r) => r.status === 200,
            'cluster state is ok': (r) => {
                const body = JSON.parse(r.body);
                return body.state === 'ok';
            },
        });
    });

    group('Create Event', function () {
        const payload = JSON.stringify({
            name: `Smoke Test Event ${Date.now()}`,
            venue: 'Test Venue',
            rows: 5,
            seats_per_row: 10,
            price_per_seat: 25.00,
        });

        const res = http.post(`${CONFIG.BASE_URL}/events`, payload, {
            headers: { 'Content-Type': 'application/json' },
        });

        check(res, {
            'create event returns 201': (r) => r.status === 201,
            'event has ID': (r) => {
                const body = JSON.parse(r.body);
                return body.id !== undefined && body.id !== '';
            },
            'event has correct seats': (r) => {
                const body = JSON.parse(r.body);
                return body.total_seats === 50;
            },
        });

        if (res.status === 201) {
            eventId = JSON.parse(res.body).id;
        }
    });

    if (eventId) {
        group('Get Event', function () {
            const res = http.get(`${CONFIG.BASE_URL}/events/${eventId}`);
            check(res, {
                'get event returns 200': (r) => r.status === 200,
                'event ID matches': (r) => {
                    const body = JSON.parse(r.body);
                    return body.id === eventId;
                },
            });
        });

        group('Get Availability', function () {
            const res = http.get(`${CONFIG.BASE_URL}/events/${eventId}/availability`);
            check(res, {
                'availability returns 200': (r) => r.status === 200,
                'all seats available': (r) => {
                    const body = JSON.parse(r.body);
                    return body.available_seats === 50;
                },
            });
        });

        group('Get Available Seats', function () {
            const res = http.get(`${CONFIG.BASE_URL}/events/${eventId}/seats`);
            check(res, {
                'seats returns 200': (r) => r.status === 200,
                'has 50 available seats': (r) => {
                    const body = JSON.parse(r.body);
                    return body.count === 50;
                },
            });
        });

        group('Create Reservation', function () {
            const payload = JSON.stringify({
                event_id: eventId,
                user_id: 'smoke_test_user',
                seats: ['A1', 'A2'],
                customer_name: 'Smoke Test',
                customer_email: 'smoke@test.com',
            });

            const res = http.post(`${CONFIG.BASE_URL}/reservations`, payload, {
                headers: { 'Content-Type': 'application/json' },
            });

            check(res, {
                'reservation returns 201': (r) => r.status === 201,
                'reservation has ID': (r) => {
                    const body = JSON.parse(r.body);
                    return body.id !== undefined;
                },
                'reservation status is pending': (r) => {
                    const body = JSON.parse(r.body);
                    return body.status === 'pending';
                },
                'reservation has correct seats': (r) => {
                    const body = JSON.parse(r.body);
                    return body.seats.length === 2;
                },
            });

            if (res.status === 201) {
                reservationId = JSON.parse(res.body).id;
            }
        });

        if (reservationId) {
            group('Get Reservation', function () {
                const res = http.get(`${CONFIG.BASE_URL}/reservations/${reservationId}`);
                check(res, {
                    'get reservation returns 200': (r) => r.status === 200,
                });
            });

            group('Verify Availability Updated', function () {
                const res = http.get(`${CONFIG.BASE_URL}/events/${eventId}/availability`);
                check(res, {
                    'availability shows pending seats': (r) => {
                        const body = JSON.parse(r.body);
                        return body.pending_seats === 2 && body.available_seats === 48;
                    },
                });
            });

            group('Confirm Reservation', function () {
                const payload = JSON.stringify({
                    payment_id: 'smoke_test_payment',
                });

                const res = http.post(
                    `${CONFIG.BASE_URL}/reservations/${reservationId}/confirm`,
                    payload,
                    { headers: { 'Content-Type': 'application/json' } }
                );

                check(res, {
                    'confirm returns 200': (r) => r.status === 200,
                    'status is confirmed': (r) => {
                        const body = JSON.parse(r.body);
                        return body.status === 'confirmed';
                    },
                });
            });

            group('Verify Final Availability', function () {
                const res = http.get(`${CONFIG.BASE_URL}/events/${eventId}/availability`);
                check(res, {
                    'shows sold seats': (r) => {
                        const body = JSON.parse(r.body);
                        return body.sold_seats === 2 && body.pending_seats === 0;
                    },
                    'revenue updated': (r) => {
                        const body = JSON.parse(r.body);
                        return body.revenue === 50.00; // 2 seats * $25
                    },
                });
            });
        }

        group('Test Conflict Detection', function () {
            // Try to book already sold seats
            const payload = JSON.stringify({
                event_id: eventId,
                user_id: 'conflict_test_user',
                seats: ['A1'], // Already sold
                customer_name: 'Conflict Test',
                customer_email: 'conflict@test.com',
            });

            const res = http.post(`${CONFIG.BASE_URL}/reservations`, payload, {
                headers: { 'Content-Type': 'application/json' },
            });

            check(res, {
                'conflict returns 409': (r) => r.status === 409,
                'error mentions seat unavailable': (r) => {
                    const body = JSON.parse(r.body);
                    return body.error && body.error.includes('not available');
                },
            });
        });
    }

    sleep(1);
}

export function teardown() {
    console.log('Smoke test completed successfully!');
}
