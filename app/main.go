package main

import (
	"fmt"
	"os"

	"ticket-reservation/cmd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	var err error
	switch command {
	case "cluster-info":
		err = cmd.ClusterInfo()
	case "create-event":
		err = cmd.CreateEvent(args)
	case "list-events":
		err = cmd.ListEvents()
	case "availability":
		err = cmd.GetAvailability(args)
	case "seat-map":
		err = cmd.ShowSeatMap(args)
	case "reserve":
		err = cmd.ReserveSeats(args)
	case "confirm":
		err = cmd.ConfirmReservation(args)
	case "cancel":
		err = cmd.CancelReservation(args)
	case "waitlist":
		err = cmd.JoinWaitlist(args)
	case "demo":
		err = cmd.RunDemo()
	case "load-test":
		err = cmd.LoadTest(args)

	// Sharding commands
	case "slot-info":
		err = cmd.SlotInfo()
	case "key-slot":
		err = cmd.KeySlot(args)
	case "hash-tag-demo":
		err = cmd.HashTagDemo()
	case "cross-slot-demo":
		err = cmd.CrossSlotDemo()
	case "analyze-distribution":
		err = cmd.AnalyzeDistribution(args)
	case "sharding-demo":
		err = cmd.ShardingDemo()
	case "reshard-demo":
		err = cmd.ReshardDemo(args)
	case "hotkey-demo":
		err = cmd.SimulateHotKey(args)
	case "migration-demo":
		err = cmd.MigrationDemo()

	case "help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`
Ticket Reservation System - Redis Cluster Lab

Usage: ticket-reservation <command> [arguments]

Commands:
  cluster-info              Show Redis cluster status

  create-event              Create a new event
    --name <name>           Event name (required)
    --venue <venue>         Venue name (default: "Main Hall")
    --rows <n>              Number of rows (default: 10)
    --seats <n>             Seats per row (default: 10)
    --price <amount>        Price per seat (default: 50.00)

  list-events               List all events

  availability <event-id>   Show event availability stats

  seat-map <event-id>       Display seat map for an event

  reserve                   Reserve seats
    --event <id>            Event ID (required)
    --user <id>             User ID (required)
    --seats <a1,a2,...>     Comma-separated seat IDs (required)
    --name <name>           Customer name
    --email <email>         Customer email

  confirm <reservation-id>  Confirm a pending reservation
    --payment <id>          Payment reference ID

  cancel <reservation-id>   Cancel a reservation

  waitlist                  Join event waitlist
    --event <id>            Event ID (required)
    --user <id>             User ID (required)
    --email <email>         Email for notification (required)
    --seats <n>             Number of seats needed (default: 1)

  demo                      Run full demonstration scenario

  load-test                 Run concurrent load test
    --event <id>            Event ID (required)
    --users <n>             Number of concurrent users (default: 50)
    --seats <n>             Seats per user (default: 2)

SHARDING COMMANDS:
  slot-info                 Show slot distribution across nodes
  key-slot <key> [key...]   Show which slot/node keys map to
  hash-tag-demo             Demonstrate hash tags for co-location
  cross-slot-demo           Demonstrate cross-slot limitations
  analyze-distribution      Analyze key distribution in cluster
    --pattern <pattern>     Key pattern (default: *)
    --limit <n>             Max keys to scan (default: 1000)
  sharding-demo             Comprehensive sharding demonstration
  reshard-demo              Learn about resharding process
  hotkey-demo               Simulate and learn about hot keys
    --duration <sec>        Test duration (default: 5)
  migration-demo            Explain key migration during resharding

Examples:
  ticket-reservation create-event --name "Rock Concert" --rows 5 --seats 10
  ticket-reservation reserve --event abc123 --user user1 --seats A1,A2
  ticket-reservation confirm res_abc123 --payment pay_xyz
  ticket-reservation demo
`)
}
