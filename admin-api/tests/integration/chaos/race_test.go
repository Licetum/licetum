//go:build chaos && race

package chaos_test

// The race detector makes the code several times slower: the latency figures of the tests are scaled by this factor.
// (The figures themselves are the ones of the acceptance criteria, checked by the build without the detector.)
const slowdown = 8
