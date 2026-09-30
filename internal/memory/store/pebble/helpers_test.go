package pebble

func chunkPrefix() []byte {
	return recordPrefix(recordChunk)
}

func evidencePrefix() []byte {
	return recordPrefix(recordEvidence)
}
