//go:build !nosqlite

package dbmod

import _ "modernc.org/sqlite" // the driver "sqlite"

var _ = addDriver(sqlite)
