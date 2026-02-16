package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ticket-reservation/cluster"
	"ticket-reservation/models"
	"ticket-reservation/service"

	"github.com/redis/go-redis/v9"
)

// ClusterInfo displays Redis cluster status
func ClusterInfo() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	return client.PrintClusterStatus()
}

// CreateEvent creates a new event
func CreateEvent(args []string) error {
	fs := flag.NewFlagSet("create-event", flag.ExitOnError)
	name := fs.String("name", "", "Event name")
	venue := fs.String("venue", "Main Hall", "Venue name")
	rows := fs.Int("rows", 10, "Number of rows")
	seats := fs.Int("seats", 10, "Seats per row")
	price := fs.Float64("price", 50.00, "Price per seat")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("event name is required")
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	eventDate := time.Now().Add(30 * 24 * time.Hour) // 30 days from now

	event, err := svc.CreateEvent(*name, *venue, eventDate, *rows, *seats, *price)
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("         EVENT CREATED")
	fmt.Println("========================================")
	fmt.Printf("Event ID:     %s\n", event.ID)
	fmt.Printf("Name:         %s\n", event.Name)
	fmt.Printf("Venue:        %s\n", event.Venue)
	fmt.Printf("Date:         %s\n", event.Date.Format("2006-01-02 15:04"))
	fmt.Printf("Total Seats:  %d (%d rows x %d seats)\n", event.TotalSeats, event.Rows, event.SeatsPerRow)
	fmt.Printf("Price/Seat:   $%.2f\n", event.PricePerSeat)
	fmt.Println("========================================")

	// Show which Redis slot this event maps to
	slot := client.GetSlotForKey(fmt.Sprintf("{event:%s}", event.ID))
	nodeAddr, _ := client.GetNodeForSlot(slot)
	fmt.Printf("\nRedis Slot: %d (Node: %s)\n", slot, nodeAddr)

	return nil
}

// ListEvents lists all events (scans cluster)
func ListEvents() error {
	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("\n========================================")
	fmt.Println("           EVENTS")
	fmt.Println("========================================")

	// Scan all nodes for event keys
	var events []*models.Event
	ctx := client.Context()

	err = client.ForEachMaster(func(c *redis.Client) error {
		iter := c.Scan(ctx, 0, "{event:*}", 100).Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			// Skip non-metadata keys (seats, stats, etc.)
			if strings.Contains(key, ":") && !strings.HasPrefix(key, "{event:") {
				continue
			}
			if strings.Count(key, ":") > 0 {
				parts := strings.Split(key, ":")
				if len(parts) > 1 && strings.Contains(parts[1], "}") {
					continue
				}
			}

			eventJSON, err := c.Get(ctx, key).Result()
			if err != nil {
				continue
			}

			var event models.Event
			if err := json.Unmarshal([]byte(eventJSON), &event); err == nil {
				events = append(events, &event)
			}
		}
		return iter.Err()
	})

	if len(events) == 0 {
		fmt.Println("No events found.")
	} else {
		for _, event := range events {
			fmt.Printf("\n[%s] %s\n", event.ID, event.Name)
			fmt.Printf("  Venue: %s | Date: %s\n", event.Venue, event.Date.Format("2006-01-02"))
			fmt.Printf("  Seats: %d | Price: $%.2f\n", event.TotalSeats, event.PricePerSeat)
		}
	}
	fmt.Println("\n========================================")

	return err
}

// GetAvailability shows event availability
func GetAvailability(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("event ID required")
	}
	eventID := args[0]

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	stats, err := svc.GetAvailability(eventID)
	if err != nil {
		return err
	}

	event, _ := svc.GetEvent(eventID)

	fmt.Println("\n========================================")
	fmt.Printf("     AVAILABILITY: %s\n", event.Name)
	fmt.Println("========================================")
	fmt.Printf("Total Seats:     %d\n", stats.TotalSeats)
	fmt.Printf("Available:       %d\n", stats.AvailableSeats)
	fmt.Printf("Pending:         %d\n", stats.PendingSeats)
	fmt.Printf("Sold:            %d\n", stats.SoldSeats)
	fmt.Printf("Waitlist:        %d\n", stats.WaitlistCount)
	fmt.Printf("Revenue:         $%.2f\n", stats.Revenue)
	fmt.Println("========================================")

	return nil
}

// ShowSeatMap displays the seat map
func ShowSeatMap(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("event ID required")
	}
	eventID := args[0]

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	return svc.PrintSeatMap(eventID)
}

// ReserveSeats reserves seats for a user
func ReserveSeats(args []string) error {
	fs := flag.NewFlagSet("reserve", flag.ExitOnError)
	eventID := fs.String("event", "", "Event ID")
	userID := fs.String("user", "", "User ID")
	seatsStr := fs.String("seats", "", "Comma-separated seat IDs")
	name := fs.String("name", "", "Customer name")
	email := fs.String("email", "", "Customer email")
	fs.Parse(args)

	if *eventID == "" || *userID == "" || *seatsStr == "" {
		return fmt.Errorf("event, user, and seats are required")
	}

	seats := strings.Split(*seatsStr, ",")
	for i, s := range seats {
		seats[i] = strings.TrimSpace(strings.ToUpper(s))
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	reservation, err := svc.ReserveSeats(*eventID, *userID, seats, *name, *email)
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("       RESERVATION CREATED")
	fmt.Println("========================================")
	fmt.Printf("Reservation ID:  %s\n", reservation.ID)
	fmt.Printf("Event ID:        %s\n", reservation.EventID)
	fmt.Printf("User ID:         %s\n", reservation.UserID)
	fmt.Printf("Seats:           %v\n", reservation.Seats)
	fmt.Printf("Total Amount:    $%.2f\n", reservation.TotalAmount)
	fmt.Printf("Status:          %s\n", reservation.Status)
	fmt.Printf("Expires At:      %s\n", reservation.ExpiresAt.Format("15:04:05"))
	fmt.Println("========================================")
	fmt.Println("\nUse 'confirm <reservation-id>' to complete the booking")

	return nil
}

// ConfirmReservation confirms a pending reservation
func ConfirmReservation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("reservation ID required")
	}

	fs := flag.NewFlagSet("confirm", flag.ExitOnError)
	paymentID := fs.String("payment", "", "Payment reference ID")
	fs.Parse(args[1:])

	reservationID := args[0]
	if *paymentID == "" {
		*paymentID = fmt.Sprintf("pay_%d", time.Now().Unix())
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	reservation, err := svc.ConfirmReservation(reservationID, *paymentID)
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("      RESERVATION CONFIRMED!")
	fmt.Println("========================================")
	fmt.Printf("Reservation ID:  %s\n", reservation.ID)
	fmt.Printf("Seats:           %v\n", reservation.Seats)
	fmt.Printf("Total Paid:      $%.2f\n", reservation.TotalAmount)
	fmt.Printf("Payment ID:      %s\n", reservation.PaymentID)
	fmt.Printf("Confirmed At:    %s\n", reservation.ConfirmedAt.Format("2006-01-02 15:04:05"))
	fmt.Println("========================================")

	return nil
}

// CancelReservation cancels a reservation
func CancelReservation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("reservation ID required")
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	err = svc.CancelReservation(args[0])
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("     RESERVATION CANCELLED")
	fmt.Println("========================================")
	fmt.Printf("Reservation %s has been cancelled.\n", args[0])
	fmt.Println("Seats have been released back to available.")
	fmt.Println("========================================")

	return nil
}

// JoinWaitlist adds user to waitlist
func JoinWaitlist(args []string) error {
	fs := flag.NewFlagSet("waitlist", flag.ExitOnError)
	eventID := fs.String("event", "", "Event ID")
	userID := fs.String("user", "", "User ID")
	email := fs.String("email", "", "Email for notification")
	seats := fs.Int("seats", 1, "Number of seats needed")
	fs.Parse(args)

	if *eventID == "" || *userID == "" || *email == "" {
		return fmt.Errorf("event, user, and email are required")
	}

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 15*time.Minute)
	entry, err := svc.JoinWaitlist(*eventID, *userID, *email, *seats)
	if err != nil {
		return err
	}

	fmt.Println("\n========================================")
	fmt.Println("       JOINED WAITLIST")
	fmt.Println("========================================")
	fmt.Printf("Waitlist ID:     %s\n", entry.ID)
	fmt.Printf("Event ID:        %s\n", entry.EventID)
	fmt.Printf("Requested Seats: %d\n", entry.RequestedSeats)
	fmt.Printf("Email:           %s\n", entry.Email)
	fmt.Println("========================================")
	fmt.Println("\nYou'll be notified when seats become available.")

	return nil
}

// RunDemo runs a full demonstration scenario
func RunDemo() error {
	fmt.Println("\n========================================")
	fmt.Println("   TICKET RESERVATION SYSTEM DEMO")
	fmt.Println("========================================")

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	// Show cluster status
	fmt.Println("\n[Step 1] Cluster Status")
	client.PrintClusterStatus()

	svc := service.NewReservationService(client.Redis(), 2*time.Minute) // Short TTL for demo

	// Create an event
	fmt.Println("\n[Step 2] Creating Event...")
	event, err := svc.CreateEvent(
		"Redis Cluster Concert",
		"Tech Arena",
		time.Now().Add(30*24*time.Hour),
		5,  // 5 rows
		10, // 10 seats per row
		75.00,
	)
	if err != nil {
		return err
	}
	fmt.Printf("Created event: %s (ID: %s)\n", event.Name, event.ID)
	fmt.Printf("Total seats: %d\n", event.TotalSeats)

	// Show slot distribution
	slot := client.GetSlotForKey(fmt.Sprintf("{event:%s}", event.ID))
	nodeAddr, _ := client.GetNodeForSlot(slot)
	fmt.Printf("Event data stored on slot %d (Node: %s)\n", slot, nodeAddr)

	// Show initial seat map
	fmt.Println("\n[Step 3] Initial Seat Map")
	svc.PrintSeatMap(event.ID)

	// Reserve some seats
	fmt.Println("\n[Step 4] Making Reservations...")

	// User 1 reserves front row
	res1, err := svc.ReserveSeats(event.ID, "user1", []string{"A1", "A2", "A3"}, "Alice", "alice@example.com")
	if err != nil {
		return fmt.Errorf("user1 reservation failed: %w", err)
	}
	fmt.Printf("User1 reserved seats A1,A2,A3 (Reservation: %s)\n", res1.ID)

	// User 2 reserves middle seats
	res2, err := svc.ReserveSeats(event.ID, "user2", []string{"C5", "C6"}, "Bob", "bob@example.com")
	if err != nil {
		return fmt.Errorf("user2 reservation failed: %w", err)
	}
	fmt.Printf("User2 reserved seats C5,C6 (Reservation: %s)\n", res2.ID)

	// Show updated seat map
	fmt.Println("\n[Step 5] Seat Map After Reservations")
	svc.PrintSeatMap(event.ID)

	// Show availability
	fmt.Println("\n[Step 6] Current Availability")
	stats, _ := svc.GetAvailability(event.ID)
	fmt.Printf("Available: %d | Pending: %d | Sold: %d\n",
		stats.AvailableSeats, stats.PendingSeats, stats.SoldSeats)

	// Confirm user1's reservation
	fmt.Println("\n[Step 7] Confirming User1's Reservation...")
	_, err = svc.ConfirmReservation(res1.ID, "pay_demo_001")
	if err != nil {
		return err
	}
	fmt.Println("User1's reservation confirmed!")

	// Show seat map after confirmation
	fmt.Println("\n[Step 8] Seat Map After Confirmation")
	svc.PrintSeatMap(event.ID)

	// Demonstrate conflict detection
	fmt.Println("\n[Step 9] Testing Conflict Detection...")
	_, err = svc.ReserveSeats(event.ID, "user3", []string{"A1", "A2"}, "Charlie", "charlie@example.com")
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	}

	// Cancel user2's reservation
	fmt.Println("\n[Step 10] Cancelling User2's Reservation...")
	err = svc.CancelReservation(res2.ID)
	if err != nil {
		return err
	}
	fmt.Println("User2's reservation cancelled, seats released!")

	// Final seat map
	fmt.Println("\n[Step 11] Final Seat Map")
	svc.PrintSeatMap(event.ID)

	// Final stats
	fmt.Println("\n[Step 12] Final Statistics")
	stats, _ = svc.GetAvailability(event.ID)
	fmt.Printf("Available: %d | Pending: %d | Sold: %d | Revenue: $%.2f\n",
		stats.AvailableSeats, stats.PendingSeats, stats.SoldSeats, stats.Revenue)

	fmt.Println("\n========================================")
	fmt.Println("          DEMO COMPLETE!")
	fmt.Println("========================================")
	fmt.Printf("\nEvent ID for further testing: %s\n", event.ID)

	return nil
}

// GetKey retrieves data by key from Redis cluster
func GetKey(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("key is required")
	}
	key := args[0]

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := client.Context()
	rdb := client.Redis()

	// Check if key exists and get its type
	keyType, err := rdb.Type(ctx, key).Result()
	if err != nil {
		return err
	}
	if keyType == "none" {
		fmt.Printf("Key '%s' not found\n", key)
		return nil
	}

	// Show slot info
	slot := client.GetSlotForKey(key)
	nodeAddr, _ := client.GetNodeForSlot(slot)

	fmt.Println("\n========================================")
	fmt.Println("           GET KEY")
	fmt.Println("========================================")
	fmt.Printf("Key:    %s\n", key)
	fmt.Printf("Type:   %s\n", keyType)
	fmt.Printf("Slot:   %d (Node: %s)\n", slot, nodeAddr)
	fmt.Println("----------------------------------------")

	// Get value based on type
	switch keyType {
	case "string":
		val, err := rdb.Get(ctx, key).Result()
		if err != nil {
			return err
		}
		fmt.Printf("Value:\n%s\n", val)

	case "list":
		vals, err := rdb.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return err
		}
		fmt.Printf("Values (%d items):\n", len(vals))
		for i, v := range vals {
			fmt.Printf("  [%d] %s\n", i, v)
		}

	case "set":
		vals, err := rdb.SMembers(ctx, key).Result()
		if err != nil {
			return err
		}
		fmt.Printf("Members (%d items):\n", len(vals))
		for _, v := range vals {
			fmt.Printf("  - %s\n", v)
		}

	case "zset":
		vals, err := rdb.ZRangeWithScores(ctx, key, 0, -1).Result()
		if err != nil {
			return err
		}
		fmt.Printf("Members (%d items):\n", len(vals))
		for _, v := range vals {
			fmt.Printf("  - %v (score: %v)\n", v.Member, v.Score)
		}

	case "hash":
		vals, err := rdb.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		fmt.Printf("Fields (%d items):\n", len(vals))
		for k, v := range vals {
			fmt.Printf("  %s: %s\n", k, v)
		}

	default:
		fmt.Printf("Unsupported type: %s\n", keyType)
	}

	fmt.Println("========================================")

	return nil
}

// LoadTest runs concurrent reservation load test
func LoadTest(args []string) error {
	fs := flag.NewFlagSet("load-test", flag.ExitOnError)
	eventID := fs.String("event", "", "Event ID (creates new if empty)")
	numUsers := fs.Int("users", 50, "Number of concurrent users")
	seatsPerUser := fs.Int("seats", 2, "Seats per user")
	fs.Parse(args)

	client, err := cluster.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := service.NewReservationService(client.Redis(), 5*time.Minute)

	// Create event if not specified
	if *eventID == "" {
		event, err := svc.CreateEvent(
			"Load Test Event",
			"Test Venue",
			time.Now().Add(30*24*time.Hour),
			20, // 20 rows
			50, // 50 seats per row = 1000 seats
			25.00,
		)
		if err != nil {
			return err
		}
		*eventID = event.ID
		fmt.Printf("Created test event: %s with 1000 seats\n", event.ID)
	}

	// Get available seats
	available, err := svc.GetAvailableSeats(*eventID)
	if err != nil {
		return err
	}
	fmt.Printf("Available seats: %d\n", len(available))

	fmt.Println("\n========================================")
	fmt.Printf("   LOAD TEST: %d users, %d seats each\n", *numUsers, *seatsPerUser)
	fmt.Println("========================================")

	var successCount int64
	var failCount int64
	var wg sync.WaitGroup
	startTime := time.Now()

	// Shuffle available seats
	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})

	// Launch concurrent users
	seatIdx := 0
	var seatMutex sync.Mutex

	for i := 0; i < *numUsers; i++ {
		wg.Add(1)
		go func(userNum int) {
			defer wg.Done()

			// Get seats for this user
			seatMutex.Lock()
			if seatIdx+*seatsPerUser > len(available) {
				seatMutex.Unlock()
				atomic.AddInt64(&failCount, 1)
				return
			}
			seats := available[seatIdx : seatIdx+*seatsPerUser]
			seatIdx += *seatsPerUser
			seatMutex.Unlock()

			userID := fmt.Sprintf("loadtest_user_%d", userNum)
			_, err := svc.ReserveSeats(*eventID, userID, seats, "", "")
			if err != nil {
				atomic.AddInt64(&failCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	fmt.Printf("\nResults:\n")
	fmt.Printf("  Duration:     %v\n", duration)
	fmt.Printf("  Successful:   %d\n", successCount)
	fmt.Printf("  Failed:       %d\n", failCount)
	fmt.Printf("  Throughput:   %.2f reservations/sec\n", float64(successCount)/duration.Seconds())

	// Show final stats
	stats, _ := svc.GetAvailability(*eventID)
	fmt.Printf("\nFinal State:\n")
	fmt.Printf("  Available: %d | Pending: %d | Sold: %d\n",
		stats.AvailableSeats, stats.PendingSeats, stats.SoldSeats)

	fmt.Println("\n========================================")

	return nil
}
