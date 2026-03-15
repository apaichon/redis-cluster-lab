// Load test configuration
export const CONFIG = {
    BASE_URL: __ENV.BASE_URL || 'http://localhost:8080',

    // Test scenarios
    SCENARIOS: {
        smoke: {
            vus: 1,
            duration: '30s',
        },
        load: {
            vus: 50,
            duration: '5m',
        },
        stress: {
            vus: 100,
            duration: '10m',
        },
        spike: {
            stages: [
                { duration: '1m', target: 10 },
                { duration: '30s', target: 100 },
                { duration: '1m', target: 100 },
                { duration: '30s', target: 10 },
                { duration: '1m', target: 0 },
            ],
        },
        soak: {
            vus: 30,
            duration: '30m',
        },
    },

    // Thresholds
    THRESHOLDS: {
        http_req_duration: ['p(95)<500', 'p(99)<1000'],
        http_req_failed: ['rate<0.01'],
        reservation_success_rate: ['rate>0.95'],
    },

    // Event configuration for tests
    EVENT: {
        rows: 20,
        seatsPerRow: 50,
        pricePerSeat: 25.00,
    },
};

// Helper to generate random seat IDs
export function getRandomSeats(rows, seatsPerRow, count) {
    const seats = [];
    const used = new Set();

    while (seats.length < count) {
        const row = String.fromCharCode(65 + Math.floor(Math.random() * rows));
        const seatNum = Math.floor(Math.random() * seatsPerRow) + 1;
        const seatId = `${row}${seatNum}`;

        if (!used.has(seatId)) {
            used.add(seatId);
            seats.push(seatId);
        }
    }

    return seats;
}

// Helper to generate unique user ID
export function generateUserId() {
    return `user_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
}
