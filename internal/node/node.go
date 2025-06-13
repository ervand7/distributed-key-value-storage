package node

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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

// API request/response structures
type (
	kvReq struct {
		Value []byte        `json:"value"`
		Ver   store.Version `json:"version"`
	}

	kvResp struct {
		Value []byte        `json:"value"`
		Ver   store.Version `json:"version"`
	}
)

type Node struct {
	ID      string
	Addr    string
	Store   *store.Store
	Ring    *consistenthash.Ring
	State   *gossip.State
	Version uint64 // local Lamport clock
}

func NewNode(id string, addr string, store *store.Store, ring *consistenthash.Ring, state *gossip.State) *Node {
	return &Node{
		ID:    id,
		Addr:  addr,
		Store: store,
		Ring:  ring,
		State: state,
	}
}

// HandleKV handles external client requests to store (PUT) or retrieve (GET) key-value pairs.
// It ensures replication across multiple nodes and maintains eventual
// consistency using versioning.
func (n *Node) HandleKV(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		n.handleKvPut(w, r)
	case http.MethodGet:
		n.handleKvGet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleInternalKV Internal replication endpoints
func (n *Node) HandleInternalKV(w http.ResponseWriter, r *http.Request) {
	var req kvReq
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Println(err)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key missing", http.StatusBadRequest)
		return
	}
	stored := n.Store.Put(store.Entry{Key: key, Value: req.Value, Ver: req.Ver})
	if stored {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (n *Node) HandleGossip(_ http.ResponseWriter, r *http.Request) {
	var st gossip.State
	err := json.NewDecoder(r.Body).Decode(&st)
	if err != nil {
		log.Println(err)
	}
	n.State.Merge(&st)
}

func (n *Node) handleKvPut(w http.ResponseWriter, r *http.Request) {
	// Extract the key from the request URL, e.g., "/kv/user42" → "user42"
	key := strings.TrimPrefix(r.URL.Path, "/kv/")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create a new version using the Lamport clock (monotonically increasing per node)
	newVer := store.Version{
		Counter: atomic.AddUint64(&n.Version, 1),
		NodeID:  n.ID,
	}

	// Create a new entry
	entry := store.Entry{Key: key, Value: body, Ver: newVer}

	// Get the list of nodes responsible for this key (replication set)
	nodes := n.Ring.Get(key, replicaFactor)

	acks := 0 // Track how many replicas acknowledged the write

	for _, nodeID := range nodes {
		if nodeID == n.ID {
			// Store locally if this node is part of the replication set
			if n.Store.Put(entry) {
				acks++
			}
			continue
		}

		// Get the address of the target node from gossip state
		targetNode := n.State.Nodes[nodeID]
		if targetNode == "" {
			// Node is unknown or offline
			continue
		}

		// Send internal replication request to peer node
		if n.sendInternalPut(targetNode, entry) {
			acks++
		}
	}

	// Check if write quorum is met (i.e., enough successful writes)
	if quorum.IsQuorum(acks, replicaFactor, writeQuorum) {
		w.WriteHeader(http.StatusCreated)
	} else {
		http.Error(w, "quorum failed", http.StatusServiceUnavailable)
	}
}

func (n *Node) handleKvGet(w http.ResponseWriter, r *http.Request) {
	// Extract the key from the request URL, e.g., "/kv/user42" → "user42"
	key := strings.TrimPrefix(r.URL.Path, "/kv/")

	// Get the nodes that should store this key
	nodes := n.Ring.Get(key, replicaFactor)

	var winner store.Entry // The latest (most recent) value by version
	acks := 0              // Successful read acknowledgments

	for _, nodeID := range nodes {
		dest := n.State.Nodes[nodeID]

		// Try to read from local store if it's us or if we don't know the peer
		if nodeID == n.ID || dest == "" {
			if entry, ok := n.Store.Get(key); ok {
				// Choose the most recent version based on version comparison
				if acks == 0 || entry.Ver.Compare(winner.Ver) > 0 {
					winner = entry
				}
				acks++
			}
			continue
		}

		// Ask another node for the value
		if entry, ok := n.sendInternalGet(dest, key); ok {
			if acks == 0 || entry.Ver.Compare(winner.Ver) > 0 {
				winner = entry
			}
			acks++
		}
	}

	// Check if read quorum is met (enough successful reads)
	if quorum.IsQuorum(acks, replicaFactor, readQuorum) {
		// Return the most recent version of the value
		err := json.NewEncoder(w).Encode(kvResp{Value: winner.Value, Ver: winner.Ver})
		if err != nil {
			log.Println(err)
		}
	} else {
		http.Error(w, "quorum failed", http.StatusServiceUnavailable)
	}
}

// sendInternalPut sends a key-value entry to a peer node for internal replication.
// It performs an HTTP POST request to the /internal/kv endpoint on the destination node,
// including the key as a query parameter and the value with version in the JSON body.
// Returns true if the remote node acknowledges the write (201 Created or 200 OK),
// indicating that the entry was successfully stored or already up to date.
func (n *Node) sendInternalPut(dest string, e store.Entry) bool {
	cli := &http.Client{Timeout: 1 * time.Second}
	query := fmt.Sprintf("http://%s/internal/kv?key=%s", dest, e.Key)
	body, err := json.Marshal(kvReq{Value: e.Value, Ver: e.Ver})
	if err != nil {
		log.Printf("[%s] failed to marshal kv request: %v", e.Key, err)
		return false
	}

	resp, err := cli.Post(query, "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Printf("[%s] failed to send kv request: %v", e.Key, err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		log.Printf("[%s] io.Copy: %v", e.Key, err)
		return false
	}

	return resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK
}

func (n *Node) sendInternalGet(dest, key string) (store.Entry, bool) {
	cli := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://%s/internal/kv?key=%s", dest, key)
	resp, err := cli.Get(url)
	if err != nil {
		return store.Entry{}, false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return store.Entry{}, false
	}

	var req kvReq
	err = json.NewDecoder(resp.Body).Decode(&req)
	if err != nil {
		log.Println(err)
		return store.Entry{}, false
	}

	return store.Entry{Key: key, Value: req.Value, Ver: req.Ver}, true
}
