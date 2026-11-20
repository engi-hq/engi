package dbpool

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Credential defaults mirror the credential types' field defaults.
const (
	defaultPostgresPort = 5432
	defaultMySQLPort    = 3306
	defaultSSLMode      = "prefer"
)

func str(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

func portOf(data map[string]any, def int) (int, error) {
	switch v := data["port"].(type) {
	case nil:
		return def, nil
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("port %q is not a number", v)
		}
		return n, nil
	}
	return 0, errors.New("port must be a number")
}

func postgresConnector(data map[string]any, connectTimeout time.Duration) (driver.Connector, error) {
	host, db, user := str(data, "host"), str(data, "database"), str(data, "user")
	if host == "" || db == "" || user == "" {
		return nil, errors.New("host, database and user are required")
	}
	p, err := portOf(data, defaultPostgresPort)
	if err != nil {
		return nil, err
	}
	mode := str(data, "sslMode")
	if mode == "" {
		mode = defaultSSLMode
	}
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(user, str(data, "password")),
		Host: net.JoinHostPort(host, strconv.Itoa(p)), Path: "/" + db,
		RawQuery: url.Values{"sslmode": {mode}}.Encode(),
	}
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		// pgx errors can echo the connection string; do not forward them.
		return nil, errors.New("invalid postgres connection settings")
	}
	cfg.ConnectTimeout = connectTimeout
	return stdlib.GetConnector(*cfg), nil
}

func mysqlConnector(data map[string]any, connectTimeout time.Duration) (driver.Connector, error) {
	host, db, user := str(data, "host"), str(data, "database"), str(data, "user")
	if host == "" || db == "" || user == "" {
		return nil, errors.New("host, database and user are required")
	}
	p, err := portOf(data, defaultMySQLPort)
	if err != nil {
		return nil, err
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.DBName = user, str(data, "password"), db
	cfg.Net, cfg.Addr = "tcp", net.JoinHostPort(host, strconv.Itoa(p))
	cfg.Timeout = connectTimeout
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.TLS, err = mysqlTLS(data, host); err != nil {
		return nil, err
	}
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating mysql connector: %w", err)
	}
	return conn, nil
}

// mysqlTLS maps the credential's tlsMode (and optional CA) to a tls.Config.
func mysqlTLS(data map[string]any, host string) (*tls.Config, error) {
	var roots *x509.CertPool
	if pem := str(data, "caCertificate"); pem != "" {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(pem)) {
			return nil, errors.New("caCertificate holds no valid PEM certificate")
		}
	}
	switch mode := str(data, "tlsMode"); mode {
	case "", "disable":
		return nil, nil
	case "required":
		return &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
	case "skip-verify":
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil //nolint:gosec // explicit user choice
	default:
		return nil, fmt.Errorf("unknown tlsMode %q", mode)
	}
}
