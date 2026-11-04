//go:build chaos && race

package chaos_test

// The race detector makes the code several times slower: the latency figures of the tests are scaled by this factor.
// (The figures themselves are the ones of the acceptance criteria, checked by the build without the detector.)
const slowdown = 8

// loadDivisor scales down the load of the tests that send hundreds of requests a second: under the detector one machine
// cannot serve them, and what is checked (no failure, a bounded pool) does not depend on the rate.
const loadDivisor = 4
