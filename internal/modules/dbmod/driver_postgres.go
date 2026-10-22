//go:build !nopostgres

package dbmod

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the driver "pgx"
)

var _ = addDriver(postgres)

func init() { checkPostgres = parsePostgres }

// parsePostgres reads a URL as the driver does: an option it does not
// take (sslmode=bogus) is an error here, before connecting. The error is
// what is wrong, without the URL (with its password).
func parsePostgres(u string) error {
	_, err := pgconn.ParseConfig(u)
	var pe *pgconn.ParseConfigError
	if errors.As(err, &pe) {
		msg := pe.Error()
		if i := strings.Index(msg, "`: "); i >= 0 { // "cannot parse `url`: what"
			msg = msg[i+3:]
		}
		return errors.New(msg)
	}
	return err
}
