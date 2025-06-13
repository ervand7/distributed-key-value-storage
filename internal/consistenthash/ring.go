package consistenthash

// Consistent hashing ring implementation.
// This ring distributes virtual nodes (replicas) around a 64‑bit hash space.
// Keys are assigned to the first replica clockwise from the key's hash.

import (
	"sort"
	"strconv"

	"github.com/cespare/xxhash/v2"
)

type Ring struct {
	replicas int
	hashMap  map[uint64]string // hash -> node ID
	keys     []uint64
}

// NewRing constructs a ring with the given number of virtual node replicas.
func NewRing(replicas int) *Ring {
	return &Ring{
		replicas: replicas,
		hashMap:  make(map[uint64]string),
	}
}

// Add inserts a node (physical) into the ring as 'replicas' virtual nodes.
func (r *Ring) Add(nodeID string) {
	for i := 0; i < r.replicas; i++ {
		h := xxhash.Sum64String(strconv.Itoa(i) + nodeID)
		r.keys = append(r.keys, h)
		r.hashMap[h] = nodeID
	}
	sort.Slice(r.keys, func(i, j int) bool { return r.keys[i] < r.keys[j] })
}

// Get returns up to 'num' distinct node IDs responsible for 'key'.
// It uses consistent hashing to find the correct virtual nodes.
func (r *Ring) Get(key string, num int) []string {
	// If the ring is empty or the requested number of nodes is incorrect - return Nil
	if len(r.keys) == 0 || num <= 0 {
		return nil
	}

	// calculate the hash from the key (xxhash - very fast and high-quality hash)
	h := xxhash.Sum64String(key)

	// look for an index of the nearest (along the ring) hash > = h
	idx := r.search(h)

	// the resulting list of IDs of real nodes (not virtual)
	res := make([]string, 0, num)

	// the map for tracking node id already added (so as not to add duplicates)
	visited := make(map[string]struct{})

	// the cycle until the right amount of unique node id has been scored
	for len(res) < num {
		// get node id corresponding to the hash in the IDX position
		nodeID := r.hashMap[r.keys[idx]]
		if _, seen := visited[nodeID]; !seen {
			res = append(res, nodeID)
			visited[nodeID] = struct{}{}
		}

		// go to the next idx in the ring (in a circle)
		idx = (idx + 1) % len(r.keys)
	}

	// return the list of node ids responsible for the key
	return res
}

// search returns smallest index of key >= h (modulo ring length).
func (r *Ring) search(h uint64) int {
	idx := sort.Search(len(r.keys), func(i int) bool { return r.keys[i] >= h })
	if idx == len(r.keys) {
		return 0
	}

	return idx
}
