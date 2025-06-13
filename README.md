# Distributed Key‑Value Store

## Features Implemented

| Concept | Where / How |
|---------|-------------|
| **Data partitioning (consistent hashing)** | [`internal/consistenthash/ring.go`](internal/consistenthash/ring.go) – 100 virtual nodes per physical node |
| **Replication (RF = 3)** | `replicaFactor = 3` in [`cmd/node/main.go`](cmd/node/main.go) |
| **Read / Write API** | `PUT /kv/{key}` & `GET /kv/{key}` exposed by each node |
| **Gossip membership** | [`internal/gossip`](internal/gossip) – 2 s heartbeat; merges timestamps |
| **Quorum (W=2, R=2)** | see `writeQuorum` / `readQuorum` constants |
| **Eventual consistency** | Conflicting versions converge via gossip + version comparison |
| **Versioning / conflict resolution** | Lightweight Lamport ticks (`store.Version`) |
| **SSTables (LSM)** | Memtable flushed to immutable JSON lines in `/data/*.sst` |
| **Syncing replicas** | Internode `/internal/kv` endpoint replicates writes |

## Running with Docker Compose

```bash
docker compose up --build
```

* Three containers (`node1`, `node2`, `node3`) will start.
* External ports: **8081**, **8082**, **8083** map to the nodes for easy testing.

### Example workflow

```bash
# Write (through node1)
echo -n "hello" | curl -XPUT --data-binary @- http://localhost:8081/kv/greeting

# Read (from any two nodes should succeed)
curl http://localhost:8082/kv/greeting
curl http://localhost:8083/kv/greeting
```

If you shut down `node2`, writes still succeed because W=2 (node1+node3).  
When the node comes back it will **gossip** the latest membership and exchange
SSTables, achieving *eventual consistency*.

## Code Walkthrough

### 1. `consistenthash.Ring`

A sorted slice of 64‑bit hashes represents the ring.  
Each physical node adds **virtual replicas** to minimise variance. `"Get"` locates
the first hash clockwise from the key, then walks forward to find **3 distinct nodes**.

### 2. Storage Engine

The `store.Store` keeps an in‑memory *memtable* (`map[string]Entry`).  
When the configurable threshold is hit, it is flushed to an **SSTable** (JSON lines
for clarity). Reads search the memtable first, then newest‑to‑oldest SSTables.

### 3. Versioning & Conflict Resolution

Every node maintains a Lamport counter.  
On **PUT** we increment the counter and include `{counter,nodeID}`.
If two replicas diverge, the higher counter **wins**.

### 4. Replication & Quorum

The coordinator (first replica in the ring) sends the write to the other replicas’  
`/internal/kv` endpoint and waits for **_W_ ACKs**. Reads gather values from replicas,
pick the **highest version**, and need **_R_ ACKs**.

### 5. Gossip

Every 2 seconds a node POSTs its `State{Nodes,TS}` to every peer.  
On receipt, a node merges the newer map.  
This removes the need for a central membership authority.

