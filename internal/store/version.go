package store

type Version struct {
	Counter uint64 `json:"counter"`
	NodeID  string `json:"node_id"`
}

func (v Version) Compare(other Version) int {
	if v.Counter > other.Counter {
		return 1
	} else if v.Counter < other.Counter {
		return -1
	}

	if v.NodeID > other.NodeID {
		return 1
	} else if v.NodeID < other.NodeID {
		return -1
	}

	return 0
}
