package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	auction "github.com/andersgrangaard/Auction/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type auctionServer struct {
	auction.UnimplementedAuctionServer
	auction.UnimplementedReplicationServer

	// node config
	id    string
	role  string
	peers []string

	leaderAddr string
	canPromote bool

	mu sync.Mutex

	startTime time.Time
	endTime   time.Time

	highestBid    int64
	highestBidder string
	isClosed      bool
	bidders       map[string]int64
}

func (s *auctionServer) Bid(ctx context.Context, req *auction.BidRequest) (*auction.BidResponse, error) {
	fmt.Printf("[%s|%s] >>> Bid RPC reached: auction=%s bidder=%s amount=%d\n",
		s.id, s.role, req.AuctionId, req.BidderId, req.Amount)

	if s.role == "leader" {
		return s.handleBidAsLeader(ctx, req)
	}

	if !s.canPromote {
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_EXCEPTION,
			Message:         "this node is a follower and cannot accept bids",
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, s.leaderAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		defer conn.Close()
		client := auction.NewAuctionClient(conn)
		resp, err := client.Bid(ctx, req)
		if err == nil {
			return resp, nil
		}
	}

	s.mu.Lock()
	s.role = "leader"
	s.mu.Unlock()
	s.logf("promoting self to leader after failing to reach %s", s.leaderAddr)

	return s.handleBidAsLeader(ctx, req)
}

func (s *auctionServer) handleBidAsLeader(ctx context.Context, req *auction.BidRequest) (*auction.BidResponse, error) {
	s.logf("Received Bid: auction=%s bidder=%s amount=%d", req.AuctionId, req.BidderId, req.Amount)

	// PHASE 1: VALIDATION (lock held)
	s.mu.Lock()

	now := time.Now()
	if now.After(s.endTime) {
		s.isClosed = true
	}

	if s.isClosed {
		s.mu.Unlock()
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_FAIL,
			Message:         "auction is closed",
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	if req.Amount <= 0 {
		s.mu.Unlock()
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_EXCEPTION,
			Message:         "amount must be > 0",
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	bidderID := req.BidderId
	amount := req.Amount

	if prev, ok := s.bidders[bidderID]; ok && amount <= prev {
		s.mu.Unlock()
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_FAIL,
			Message:         fmt.Sprintf("bid must be higher than your previous bid (%d)", prev),
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	if amount <= s.highestBid {
		s.mu.Unlock()
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_FAIL,
			Message:         fmt.Sprintf("bid must be higher than current highest bid (%d)", s.highestBid),
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	auctionID := req.AuctionId
	s.mu.Unlock()

	// PHASE 2: REPLICATION (lock NOT held)
	okMajority, replMsg := s.replicateBidToFollowers(ctx, auctionID, bidderID, amount)

	s.logf("replication result: ok=%v msg=%s", okMajority, replMsg)

	if !okMajority {
		return &auction.BidResponse{
			Outcome:         auction.BidOutcome_EXCEPTION,
			Message:         "replication failed: " + replMsg,
			HighestBid:      s.highestBid,
			HighestBidderId: s.highestBidder,
		}, nil
	}

	// PHASE 3: COMMIT (lock held again)
	s.mu.Lock()
	s.bidders[bidderID] = amount
	s.highestBid = amount
	s.highestBidder = bidderID

	s.logf("bid committed: highest_bid=%d highest_bidder=%s", s.highestBid, s.highestBidder)

	s.mu.Unlock()

	return &auction.BidResponse{
		Outcome:         auction.BidOutcome_SUCCESS,
		Message:         "bid accepted and replicated",
		HighestBid:      amount,
		HighestBidderId: bidderID,
	}, nil
}

// Auction RPC: Result

func (s *auctionServer) Result(ctx context.Context, req *auction.ResultRequest) (*auction.ResultResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if now.After(s.endTime) {
		s.isClosed = true
	}

	if s.isClosed {
		msg := "auction closed"
		if s.highestBidder != "" {
			msg = fmt.Sprintf("auction closed, winner %s with bid %d", s.highestBidder, s.highestBid)
		}
		return &auction.ResultResponse{
			IsClosed:   true,
			HighestBid: s.highestBid,
			WinnerId:   s.highestBidder,
			Message:    msg,
		}, nil
	}

	msg := "auction is open"
	if s.highestBid > 0 {
		msg = fmt.Sprintf("auction is open, highest bid %d by %s", s.highestBid, s.highestBidder)
	}
	return &auction.ResultResponse{
		IsClosed:   false,
		HighestBid: s.highestBid,
		WinnerId:   s.highestBidder,
		Message:    msg,
	}, nil
}

func (s *auctionServer) AppendBid(ctx context.Context, req *auction.AppendBidRequest) (*auction.AppendBidResponse, error) {
	s.logf("AppendBid from leader: auction=%s bidder=%s amount=%d", req.AuctionId, req.BidderId, req.Amount)

	s.mu.Lock()
	defer s.mu.Unlock()

	bidderID := req.BidderId
	amount := req.Amount

	s.bidders[bidderID] = amount
	s.highestBid = amount
	s.highestBidder = bidderID

	s.logf("state updated: highest_bid=%d highest_bidder=%s", s.highestBid, s.highestBidder)

	return &auction.AppendBidResponse{
		Success: true,
		Message: "bid applied on follower",
	}, nil
}

func (s *auctionServer) replicateBidToFollowers(ctx context.Context, auctionID, bidderID string, amount int64) (bool, string) {
	if len(s.peers) == 0 {
		return true, "no peers, single-node cluster"
	}

	totalNodes := len(s.peers) + 1 // leader + followers
	needed := totalNodes/2 + 1

	successes := 1
	var lastErr error

	for _, addr := range s.peers {
		peerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

		conn, err := grpc.DialContext(peerCtx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = fmt.Errorf("dial %s: %w", addr, err)
			cancel()
			continue
		}

		client := auction.NewReplicationClient(conn)
		resp, err := client.AppendBid(peerCtx, &auction.AppendBidRequest{
			AuctionId: auctionID,
			BidderId:  bidderID,
			Amount:    amount,
		})
		_ = conn.Close()
		cancel()

		if err != nil {
			lastErr = fmt.Errorf("AppendBid to %s: %w", addr, err)
			continue
		}
		if resp.Success {
			successes++
		} else {
			lastErr = fmt.Errorf("AppendBid to %s failed: %s", addr, resp.Message)
		}
	}

	if successes >= needed {
		return true, fmt.Sprintf("majority reached (%d/%d)", successes, totalNodes)
	}

	if lastErr != nil {
		return false, lastErr.Error()
	}
	return false, "not enough followers responded"
}

func (s *auctionServer) logf(format string, args ...interface{}) {
	prefix := fmt.Sprintf("[%s|%s] ", s.id, s.role)
	fmt.Printf(prefix+format+"\n", args...)
}

func main() {
	leaderAddr := flag.String("leader_addr", "127.0.0.1:50051", "initial leader address")
	canPromote := flag.Bool("can_promote", false, "whether this node can become leader if the current one fails")
	id := flag.String("id", "node1", "node id")
	role := flag.String("role", "leader", "role: leader or follower")
	port := flag.String("port", "50051", "listen port")
	peersFlag := flag.String("peers", "", "comma-separated list of follower addresses (for leader), e.g. 127.0.0.1:50052,127.0.0.1:50053")
	flag.Parse()

	var peers []string
	if *peersFlag != "" {
		for _, p := range strings.Split(*peersFlag, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
	}

	fmt.Printf("Auction server %s starting as %s on port %s...\n", *id, *role, *port)

	lis, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		panic(err)
	}

	grpcServer := grpc.NewServer()
	srv := &auctionServer{
		id:         *id,
		role:       *role,
		peers:      peers,
		leaderAddr: *leaderAddr,
		canPromote: *canPromote,
		startTime:  time.Now(),
		endTime:    time.Now().Add(100 * time.Second),
		bidders:    make(map[string]int64),
	}

	auction.RegisterAuctionServer(grpcServer, srv)
	auction.RegisterReplicationServer(grpcServer, srv)

	fmt.Printf("Server %s (%s) is listening on 127.0.0.1:%s\n", *id, *role, *port)
	if err := grpcServer.Serve(lis); err != nil {
		panic(err)
	}
}
