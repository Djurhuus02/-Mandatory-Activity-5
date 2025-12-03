package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	auction "github.com/andersgrangaard/Auction/grpc"

)

const defaultAddr = "localhost:50051"

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "bid":
		runBid(os.Args[2:])
	case "result":
		runResult(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
	}
}

func usage() {
	fmt.Println(`Usage:
  # place 'bid' or 'result' as first argument

  go run ./client bid    -addr HOST:PORT -auction_id ID -bidder_id ID -amount N
  go run ./client result -addr HOST:PORT -auction_id ID

Examples:
  go run ./client bid    -addr localhost:50051 -auction_id a1 -bidder_id b1 -amount 50
  go run ./client result -addr localhost:50051 -auction_id a1`)
}

// ---------- bid ----------

func runBid(args []string) {
	fs := flag.NewFlagSet("bid", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "gRPC server address")
	auctionID := fs.String("auction_id", "auction-1", "auction identifier")
	bidderID := fs.String("bidder_id", "bidder-1", "bidder identifier")
	amount := fs.Int64("amount", 0, "bid amount (int64)")
	_ = fs.Parse(args)

	if *amount <= 0 {
		log.Fatalf("amount must be > 0")
	}

	conn, err := grpc.Dial(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", *addr, err)
	}
	defer conn.Close()

	client := auction.NewAuctionClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.Bid(ctx, &auction.BidRequest{
		AuctionId: *auctionID,
		BidderId:  *bidderID,
		Amount:    *amount, 
	})
	if err != nil {
		log.Fatalf("Bid RPC failed: %v", err)
	}

	fmt.Printf("Bid response:\n")
	fmt.Printf("  outcome        : %s\n", resp.Outcome.String())
	fmt.Printf("  message        : %s\n", resp.Message)
	fmt.Printf("  highest_bid    : %d\n", resp.HighestBid)
	fmt.Printf("  highest_bidder : %s\n", resp.HighestBidderId)
}

// ---------- result ----------

func runResult(args []string) {
	fs := flag.NewFlagSet("result", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "gRPC server address")
	auctionID := fs.String("auction_id", "auction-1", "auction identifier")
	_ = fs.Parse(args)

	conn, err := grpc.Dial(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", *addr, err)
	}
	defer conn.Close()

	client := auction.NewAuctionClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.Result(ctx, &auction.ResultRequest{
		AuctionId: *auctionID,
	})
	if err != nil {
		log.Fatalf("Result RPC failed: %v", err)
	}

	fmt.Printf("Result:\n")
	fmt.Printf("  is_closed  : %v\n", resp.IsClosed)
	fmt.Printf("  highest_bid: %d\n", resp.HighestBid)
	fmt.Printf("  winner_id  : %s\n", resp.WinnerId)
	fmt.Printf("  message    : %s\n", resp.Message)
}
