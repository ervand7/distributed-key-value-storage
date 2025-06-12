package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"distributed-key-value-storage/internal/consistenthash"
	"distributed-key-value-storage/internal/gossip"
	"distributed-key-value-storage/internal/quorum"
	"distributed-key-value-storage/internal/store"
)

const (
	replicaFactor = 3
	writeQuorum   = 2
	readQuorum    = 2
)

type Node struct {
	id      string
	addr    string
	store   *store.Store
	ring    *consistenthash.Ring
	state   *gossip.State
	version uint64 // local Lamport clock
}

func main() {
	id := envOr("NODE_ID", "node1")
	addr := envOr("NODE_ADDR", "localhost:8080")
	peersEnv := os.Getenv("PEERS")
	peers := []string{}
	if peersEnv != "" {
		peers = strings.Split(peersEnv, ",")
	}

	n := &Node{
		id:    id,
		addr:  addr,
		store: store.NewStore("/data", 1000),
		ring:  consistenthash.NewRing(100),
		state: gossip.NewState(id, addr),
	}

	// add ourselves and peers to the ring
	n.ring.Add(id)
	for _, p := range peers {
		parts := strings.Split(p, ":")
		n.ring.Add(parts[0]) // Assume nodeID == host in compose
	}

	// start gossip
	gossip.Start(id, n.state, peers, 2*time.Second)

	// HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/kv/", n.handleKV)
	mux.HandleFunc("/internal/kv", n.handleInternalKV)
	mux.HandleFunc("/gossip", n.handleGossip)

	log.Printf("[%s] starting on %s", id, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// API request/response structures
type kvReq struct {
	Value []byte        `json:"value"`
	Ver   store.Version `json:"version"`
}

type kvResp struct {
	Value []byte        `json:"value"`
	Ver   store.Version `json:"version"`
}

// handleKV handles client PUT/GET.
func (n *Node) handleKV(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		// increment local clock
		newVer := store.Version{Counter: atomic.AddUint64(&n.version, 1), NodeID: n.id}
		e := store.Entry{Key: key, Value: body, Ver: newVer}
		// replicate
		nodes := n.ring.Get(key, replicaFactor)
		acks := 0
		for _, nodeID := range nodes {
			if nodeID == n.id {
				if n.store.Put(e) {
					acks++
				}
				continue
			}
			dest := n.state.Nodes[nodeID]
			if dest == "" {
				continue
			}
			if n.sendInternalPut(dest, e) {
				acks++
			}
		}
		if quorum.IsQuorum(acks, replicaFactor, writeQuorum) {
			w.WriteHeader(http.StatusCreated)
		} else {
			http.Error(w, "quorum failed", http.StatusServiceUnavailable)
		}
	case http.MethodGet:
		nodes := n.ring.Get(key, replicaFactor)
		var winner store.Entry
		acks := 0
		for _, nodeID := range nodes {
			dest := n.state.Nodes[nodeID]
			if nodeID == n.id || dest == "" {
				if e, ok := n.store.Get(key); ok {
					if acks == 0 || e.Ver.Compare(winner.Ver) > 0 {
						winner = e
					}
					acks++
				}
				continue
			}
			if e, ok := n.sendInternalGet(dest, key); ok {
				if acks == 0 || e.Ver.Compare(winner.Ver) > 0 {
					winner = e
				}
				acks++
			}
		}
		if quorum.IsQuorum(acks, replicaFactor, readQuorum) {
			json.NewEncoder(w).Encode(kvResp{Value: winner.Value, Ver: winner.Ver})
		} else {
			http.Error(w, "quorum failed", http.StatusServiceUnavailable)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Internal replication endpoints
func (n *Node) handleInternalKV(w http.ResponseWriter, r *http.Request) {
	var req kvReq
	json.NewDecoder(r.Body).Decode(&req)
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key missing", http.StatusBadRequest)
		return
	}
	stored := n.store.Put(store.Entry{Key: key, Value: req.Value, Ver: req.Ver})
	if stored {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (n *Node) handleGossip(w http.ResponseWriter, r *http.Request) {
	var st gossip.State
	json.NewDecoder(r.Body).Decode(&st)
	n.state.Merge(&st)
}

// HTTP helpers
func (n *Node) sendInternalPut(dest string, e store.Entry) bool {
	cli := &http.Client{Timeout: 1 * time.Second}
	query := fmt.Sprintf("http://%s/internal/kv?key=%s", dest, e.Key)
	body, _ := json.Marshal(kvReq{Value: e.Value, Ver: e.Ver})
	resp, err := cli.Post(query, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK
}

func (n *Node) sendInternalGet(dest, key string) (store.Entry, bool) {
	cli := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://%s/internal/kv?key=%s", dest, key)
	resp, err := cli.Get(url)
	if err != nil {
		return store.Entry{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return store.Entry{}, false
	}
	var req kvReq
	json.NewDecoder(resp.Body).Decode(&req)
	return store.Entry{Key: key, Value: req.Value, Ver: req.Ver}, true
}
