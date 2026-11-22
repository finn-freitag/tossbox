// Package integration contains end-to-end tests of the dashboard (REST API,
// gRPC agent protocol, scheduler, store) against a real PostgreSQL.
//
// Run with TOSSBOX_TEST_DATABASE_URL pointing to a server where the test may
// create databases, e.g. postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable.
// Without it the tests are skipped.
package integration
