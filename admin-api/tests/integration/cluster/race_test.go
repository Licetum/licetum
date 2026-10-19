//go:build integration && race

package cluster_test

// raceFactor scales the latency limits of the tests: the race detector makes the code several times slower.
const raceFactor = 4
