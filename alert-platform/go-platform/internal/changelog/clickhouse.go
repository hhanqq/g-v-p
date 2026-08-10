package changelog

import (
	"net/url"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ConnectClickHouse парсит DSN вида
// clickhouse://user:pass@host:9000/database вручную вместо того, чтобы
// полагаться на конкретную сигнатуру DSN-хелпера драйвера — меньше
// риска несовпадения версий, тот же принцип "явное лучше неявного",
// что и у остального Go-кода в этом репозитории.
func ConnectClickHouse(dsn string) (clickhouse.Conn, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	password, _ := parsed.User.Password()
	database := strings.TrimPrefix(parsed.Path, "/")
	return clickhouse.Open(&clickhouse.Options{
		Addr: []string{parsed.Host},
		Auth: clickhouse.Auth{
			Database: database,
			Username: parsed.User.Username(),
			Password: password,
		},
	})
}
