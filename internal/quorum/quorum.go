package quorum

// Helpers for quorum logic.
func IsQuorum(acks, total, required int) bool {
    return acks >= required && total >= required
}
