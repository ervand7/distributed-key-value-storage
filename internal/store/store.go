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
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	flushThreshold = 1000
)

type (
	Store struct {
		mu             sync.RWMutex
		memtable       map[string]Entry
		dir            string
		flushThreshold int
	}

	Entry struct {
		Key     string  `json:"key"`
		Value   []byte  `json:"value"`
		Version Version `json:"version"`
	}
)

// NewStore returns a Store that writes SSTables into dir.
func NewStore(ssTablesDir string) *Store {
	return &Store{
		memtable:       make(map[string]Entry),
		dir:            ssTablesDir,
		flushThreshold: flushThreshold,
	}
}

// Put updates a key if the version beats the existing one.
// Returns true if stored.
func (s *Store) Put(entry Entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.memtable[entry.Key]; ok {
		if entry.Version.Compare(e.Version) <= 0 {
			return false
		}
	}

	s.memtable[entry.Key] = entry
	if len(s.memtable) >= s.flushThreshold {
		err := s.writeToSSTable()
		if err != nil {
			log.Println(err)
		}
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

	// Get a list of SSTable files (*.sst) from the storage directory
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.sst"))

	// Sort the file names in reverse order (newest files first)
	// so we check the latest flushed SSTables before older ones
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	// Iterate through SSTables one by one
	for _, file := range files {
		// Try to find the key in this SSTable
		if e, ok := s.readFromSSTable(file, key); ok {
			return e, true // Return immediately if found
		}
	}

	// If the key wasn't found in either memtable or any SSTable
	return Entry{}, false
}

// writes the current memtable to a new SSTable file on disk, then clears the memtable.
func (s *Store) writeToSSTable() error {
	// If memtable is empty, no need to flush.
	if len(s.memtable) == 0 {
		return nil
	}

	// Create a snapshot (copy) of all current entries to avoid mutating the original
	// map while writing.
	snap := make([]Entry, 0, len(s.memtable))
	for _, e := range s.memtable {
		snap = append(snap, e)
	}

	// Sort the snapshot entries by key so the SSTable is ordered (helpful for
	// future optimizations).
	sort.Slice(snap, func(i, j int) bool { return snap[i].Key < snap[j].Key })

	// Generate a unique filename based on current timestamp (nanoseconds).
	ts := time.Now().UnixNano()
	path := filepath.Join(s.dir, fmt.Sprintf("%d.sst", ts))

	// Create the SSTable file on disk.
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	// Create a buffered writer for efficient disk I/O.
	w := bufio.NewWriter(file)

	// Create a JSON encoder to serialize entries line-by-line.
	encoder := json.NewEncoder(w)

	// Write each entry in the snapshot as a separate JSON line.
	for _, e := range snap {
		_ = encoder.Encode(e) // intentionally ignoring error for simplicity
	}

	// Flush buffered data to the file.
	err = w.Flush()
	if err != nil {
		return err
	}

	// Clear the memtable after a successful flush
	s.memtable = make(map[string]Entry)

	return nil
}

// readFromSSTable scans file line by line until key found.
func (s *Store) readFromSSTable(path string, key string) (Entry, bool) {
	file, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer func() { _ = file.Close() }()

	decoder := json.NewDecoder(file)
	var e Entry
	for decoder.More() {
		if err := decoder.Decode(&e); err == nil && e.Key == key {
			return e, true
		}
	}

	return Entry{}, false
}
