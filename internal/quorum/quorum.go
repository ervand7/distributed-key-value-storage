package quorum

func IsQuorum(acknowledgements, total, required int) bool {
	return acknowledgements >= required && total >= required
}
