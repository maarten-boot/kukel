//go:build !nopostgres

package dbmod

import _ "github.com/jackc/pgx/v5/stdlib" // the driver "pgx"

var _ = addDriver(postgres)
