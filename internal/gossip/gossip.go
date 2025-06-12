package gossip

// Extremely lightweight gossip implementation.
// Each node periodically POSTs its membership map (ID -> address)
// to a /gossip endpoint on each peer. On receipt, we merge maps.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type State struct {
	Nodes map[string]string `json:"nodes"` // nodeID -> addr
	TS    int64             `json:"ts"`    // UnixNano
	mu    sync.RWMutex
}

func NewState(selfID, selfAddr string) *State {
	return &State{
		Nodes: map[string]string{selfID: selfAddr},
		TS:    time.Now().UnixNano(),
	}
}

// Merge integrates remote state if newer.
func (s *State) Merge(remote *State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remote.TS <= s.TS {
		return
	}
	for k, v := range remote.Nodes {
		s.Nodes[k] = v
	}
	s.TS = remote.TS
}

// Start launches a goroutine that gossips every gossipInterval.
func Start(selfID string, st *State, peers []string, gossipInterval time.Duration) {
	go func() {
		cli := &http.Client{Timeout: 2 * time.Second}
		for {
			st.mu.RLock()
			payload, _ := json.Marshal(st)
			st.mu.RUnlock()
			for _, p := range peers {
				url := "http://" + p + "/gossip"
				_, _ = cli.Post(url, "application/json", bytes.NewReader(payload))
			}
			time.Sleep(gossipInterval)
		}
	}()
}
