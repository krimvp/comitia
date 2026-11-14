// Package sqlstore owns the SQLite implementation of the Store interfaces: the
// queries per domain, the migrations and backfills (migrations.go), and the
// private file checks. It imports internal/model only and opens the driver by
// name, so it never links one; package sqlite registers it.
//
// testaccess.go exports internals for tests in other packages; production
// code does not use it.
package sqlstore
