//go:build chaos && !race

package chaos_test

// slowdown scales the latency figures of the tests: 1 without the race detector (see race_test.go).
const slowdown = 1

// loadDivisor is 1 without the race detector (see race_test.go).
const loadDivisor = 1
