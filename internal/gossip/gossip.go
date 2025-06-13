package gossip

// Extremely lightweight gossip implementation.
// Each node periodically POSTs its membership map (ID -> address)
// to a /gossip endpoint on each peer. On receipt, we merge maps.

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type State struct {
	Nodes map[string]string `json:"nodes"` // nodeID -> addr
	TS    int64             `json:"ts"`    // UnixNano
	mu    sync.RWMutex
}

func NewState(id, addr string) *State {
	return &State{
		Nodes: map[string]string{id: addr},
		TS:    time.Now().UnixNano(),
	}
}

// Merge integrates `other` state if newer.
func (s *State) Merge(other *State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if other.TS <= s.TS {
		return
	}

	for k, v := range other.Nodes {
		s.Nodes[k] = v
	}
	s.TS = other.TS
}

// Start launches a goroutine that gossips every gossipInterval.
func Start(nodeID string, st *State, peers []string, gossipInterval time.Duration) {
	cli := &http.Client{Timeout: 2 * time.Second}
	for {
		log.Printf("gossiping for nodeID %s", nodeID)

		st.mu.RLock()
		payload, err := json.Marshal(st)
		if err != nil {
			log.Println(err)
			continue
		}
		st.mu.RUnlock()

		for _, p := range peers {
			url := "http://" + p + "/gossip"
			_, _ = cli.Post(url, "application/json", bytes.NewReader(payload))
		}

		time.Sleep(gossipInterval)
	}
}
