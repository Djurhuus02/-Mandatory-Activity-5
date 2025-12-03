## How to run

### Terminal 1 – Leader

```bash
go run ./server \
  -id l1 \
  -role leader \
  -port 50051 \
  -peers 127.0.0.1:50052,127.0.0.1:50053 \
  -leader_addr 127.0.0.1:50051
```

### Terminal 2 – Follower 1

```bash
o run ./server \
  -id f1 \
  -role follower \
  -port 50052 \
  -peers 127.0.0.1:50051,127.0.0.1:50053 \
  -leader_addr 127.0.0.1:50051 \
  -can_promote=true

# vanlig follower
go run ./server \
  -id f2 \
  -role follower \
  -port 50053 \
  -peers 127.0.0.1:50051,127.0.0.1:50052 \
  -leader_addr 127.0.0.1:50051
```

### Terminal 3 – Follower 2

```bash
go run ./server \
  -id f2 \
  -role follower \
  -port 50053 \
  -peers 127.0.0.1:50051,127.0.0.1:50052 \
  -leader_addr 127.0.0.1:50051

```

### Client – place a bid (can be run in a fourth terminal)

```bash
go run ./client bid   -addr=127.0.0.1:50051   -auction_id=a1   -bidder_id=b1   -amount=10
```

### Client – get result

```bash
go run ./client result   -addr=127.0.0.1:50051   -auction_id=a1
```
