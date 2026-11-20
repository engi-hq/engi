//go:build integration

package mysql_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	mycred "github.com/engi-hq/engi/internal/credtype/mysql"
	"github.com/engi-hq/engi/internal/node/mysql"
	"github.com/engi-hq/engi/internal/node/sqlnode/sqlsuite"
	"github.com/engi-hq/engi/internal/port"
)

func TestMySQLIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("engi"), tcmysql.WithUsername("engi"), tcmysql.WithPassword("secret"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting mysql container: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatal(err)
	}
	sqlsuite.Run(t, sqlsuite.Env{
		Driver: port.DriverMySQL, CredType: mycred.Name, New: mysql.New, Timestamp: "2024-05-06 07:08:09",
		Cred:        map[string]any{"host": host, "port": float64(p.Num()), "database": "engi", "user": "engi", "password": "secret", "tlsMode": "disable"},
		Placeholder: func(int) string { return "?" },
		DDL: []string{
			"CREATE TABLE IF NOT EXISTS engi_people (id INT PRIMARY KEY, name VARCHAR(100) NOT NULL, age INT, note TEXT) ENGINE=InnoDB",
			"CREATE TABLE IF NOT EXISTS engi_types (id INT PRIMARY KEY, big BIGINT, `dec` DECIMAL(10,2), ts DATETIME, doc JSON) ENGINE=InnoDB",
		},
	})
}
