// Package api_test holds the integration tests of the HTTP APIs. The tests carry the integration build tag and need Docker
// (Testcontainers), or the LICETUM_TEST_POSTGRES_URI and LICETUM_TEST_REDIS_URI environment variables pointing at running
// servers (see api_test.go). This file has no tag, so that the linters always find a package to check.
package api_test
