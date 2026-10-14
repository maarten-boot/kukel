//go:build !nomysql

package dbmod

import (
	"net"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
)

var _ = addDriver(mysqlDB)

func init() { mysqlDSN = dsnOfMySQL }

// dsnOfMySQL turns "mysql://user:password@host:port/database?options"
// into the driver's DSN ("user:password@tcp(host:port)/database?...").
func dsnOfMySQL(p *url.URL) string {
	c := mysql.NewConfig()
	c.User = p.User.Username()
	c.Passwd, _ = p.User.Password()
	c.Net, c.Addr = "tcp", p.Host
	if p.Port() == "" {
		c.Addr = net.JoinHostPort(p.Hostname(), "3306")
	}
	c.DBName = strings.TrimPrefix(p.Path, "/")
	c.ParseTime = true       // DATETIME and TIMESTAMP give times
	c.ClientFoundRows = true // an UPDATE counts the rows it finds, as in SQLite and PostgreSQL
	dsn := c.FormatDSN()
	if p.RawQuery != "" { // options such as tls=true: the driver reads them
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + p.RawQuery
	}
	return dsn
}
