package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"distributed-key-value-storage/internal/consistenthash"
	"distributed-key-value-storage/internal/gossip"
	"distributed-key-value-storage/internal/node"
	"distributed-key-value-storage/internal/store"
)

const (
	ssTablesDir string = "/data"
)

func main() {
	id := envOr("NODE_ID", "node1")
	addr := envOr("NODE_ADDR", "localhost:8080")
	peersEnv := os.Getenv("PEERS")
	peers := []string{}
	if peersEnv != "" {
		peers = strings.Split(peersEnv, ",")
	}

	err := os.MkdirAll(ssTablesDir, 0o755)
	if err != nil {
		log.Fatal(err)
	}

	n := node.NewNode(
		id,
		addr,
		store.NewStore(ssTablesDir),
		consistenthash.NewRing(100),
		gossip.NewState(id, addr),
	)

	// add ourselves and peers to the ring
	n.Ring.Add(id)
	for _, p := range peers {
		parts := strings.Split(p, ":")
		n.Ring.Add(parts[0]) // Assume nodeID == host in compose
	}

	// start gossip
	go gossip.Start(id, n.State, peers, 2*time.Second)

	// HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/kv/", n.HandleKV)
	mux.HandleFunc("/internal/kv", n.HandleInternalKV)
	mux.HandleFunc("/gossip", n.HandleGossip)

	log.Printf("[%s] starting on %s", id, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
