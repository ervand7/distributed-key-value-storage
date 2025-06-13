# 🔑⚡ Distributed Key-Value Store ⚡🔑

*A tiny Dynamo-style playground in Go — consistent hashing, quorum replication, Lamport versioning, and LSM persistence.*

---

## ✨ Feature Matrix

| 🧩 Concept                            | 💡 Where / How                                                          |
| ------------------------------------- | ----------------------------------------------------------------------- |
| **Partitioning (consistent hashing)** | `internal/consistenthash/ring.go` — 100 virtual nodes per physical node |
| **Replication (RF = 3)**              | `replicaFactor = 3` in `internal/node/node.go`                          |
| **Read / Write API**                  | `PUT /kv/{key}` & `GET /kv/{key}` exposed by every node                 |
| **Gossip membership**                 | `internal/gossip` — 2 s heartbeat, timestamp merge                      |
| **Quorum (W = 2, R = 2)**             | see `writeQuorum` & `readQuorum` constants                              |
| **Eventual consistency**              | Conflicts converge via gossip + Lamport comparison                      |
| **Versioning**                        | Lightweight Lamport ticks (`store.Version{counter,nodeID}`)             |
| **SSTables (LSM)**                    | Memtable flushes to immutable JSON-line files `/data/*.sst`             |
| **Replica sync**                      | Nodes POST to `/internal/kv` to replicate writes                        |

---

## 🐳 Quick Start (Docker Compose)

```bash
docker compose up --build
```

Three containers start:

| Node  | Port |
| ----- | ---- |
| node1 | 8081 |
| node2 | 8082 |
| node3 | 8083 |

---

### 🔄 Example Workflow

```bash
# Write through node1
echo -n "hello" | curl -X PUT --data-binary @- http://localhost:8081/kv/greeting

# Read from any two nodes (quorum read)
curl http://localhost:8082/kv/greeting
curl http://localhost:8083/kv/greeting
```

Stop **node2** — writes still succeed (W = 2).
Bring it back — nodes gossip state & exchange SSTables, restoring *eventual consistency*.

---

## 🏗️ Architecture Walkthrough

1. **Ring**
   Sorted 64-bit hash slice; each physical node adds 100 virtual nodes.
2. **Storage Engine**
   In-memory memtable → flush to JSON-line SSTable on threshold; reads search memtable then newest→oldest SSTables.
3. **Versioning & Conflict Resolution**
   Each `PUT` bumps a Lamport counter; higher `{counter,nodeID}` wins.
4. **Replication & Quorum**
   Coordinator forwards to replicas’ `/internal/kv`; waits for **W** acks (write) / **R** acks (read).
5. **Gossip**
   Every 2 s nodes POST `State{Nodes,TS}` to peers, replacing older maps — no central membership service needed.

---

### Inspiration

* **Amazon Dynamo** – quorum + vector-clock ideas
* **Cassandra / HBase** – LSM-tree storage model
* **Scuttlebutt** – ultra-simple gossip

---

**Hack away — and may your replicas always converge! 🚀**
