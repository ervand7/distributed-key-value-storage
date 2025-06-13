package store

// Very small‑footprint key‑value storage engine with
//  * in‑memory memtable
//  * immutable on‑disk SSTables
//  * basic versioning for conflict resolution
//
// This illustrates the concept used by Dynamo/Cassandra/HBase.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Version struct {
	Counter uint64 `json:"counter"`
	NodeID  string `json:"node_id"`
}

// Compare returns +1 if v > o, -1 if v < o, 0 if equal.
func (v Version) Compare(o Version) int {
	if v.Counter > o.Counter {
		return 1
	} else if v.Counter < o.Counter {
		return -1
	}
	if v.NodeID > o.NodeID {
		return 1
	} else if v.NodeID < o.NodeID {
		return -1
	}
	return 0
}

type Entry struct {
	Key   string  `json:"key"`
	Value []byte  `json:"value"`
	Ver   Version `json:"version"`
}

type Store struct {
	mu             sync.RWMutex
	memtable       map[string]Entry
	dir            string
	flushThreshold int
}

// NewStore returns a Store that writes SSTables into dir.
func NewStore(dir string, flushThreshold int) *Store {
	_ = os.MkdirAll(dir, 0o755)
	return &Store{
		memtable:       make(map[string]Entry),
		dir:            dir,
		flushThreshold: flushThreshold,
	}
}

// Put updates a key if the version beats the existing one.
// Returns true if stored.
func (s *Store) Put(e Entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.memtable[e.Key]; ok {
		if e.Ver.Compare(cur.Ver) <= 0 {
			return false
		}
	}
	s.memtable[e.Key] = e
	if len(s.memtable) >= s.flushThreshold {
		_ = s.flush()
	}
	return true
}

// Get retrieves from memtable then SSTables (newest‑first).
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.RLock()
	if e, ok := s.memtable[key]; ok {
		s.mu.RUnlock()
		return e, true
	}
	s.mu.RUnlock()

	// search latest SSTables first
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.sst"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, f := range files {
		if e, ok := readFromSSTable(f, key); ok {
			return e, true
		}
	}
	return Entry{}, false
}

// flush writes memtable to a new SSTable then clears it.
func (s *Store) flush() error {
	if len(s.memtable) == 0 {
		return nil
	}
	// capture snapshot
	snap := make([]Entry, 0, len(s.memtable))
	for _, e := range s.memtable {
		snap = append(snap, e)
	}
	sort.Slice(snap, func(i, j int) bool { return snap[i].Key < snap[j].Key })
	ts := time.Now().UnixNano()
	path := filepath.Join(s.dir, fmt.Sprintf("%d.sst", ts))
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(file)
	enc := json.NewEncoder(w)
	for _, e := range snap {
		_ = enc.Encode(e)
	}
	_ = w.Flush()
	_ = file.Close()
	// clear memtable
	s.memtable = make(map[string]Entry)
	return nil
}

// readFromSSTable scans file line by line until key found.
func readFromSSTable(path string, key string) (Entry, bool) {
	file, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	var e Entry
	for dec.More() {
		if err := dec.Decode(&e); err == nil && e.Key == key {
			return e, true
		}
	}
	return Entry{}, false
}
