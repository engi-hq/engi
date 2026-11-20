//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	pgcred "github.com/engi-hq/engi/internal/credtype/postgres"
	"github.com/engi-hq/engi/internal/node/postgres"
	"github.com/engi-hq/engi/internal/node/sqlnode/sqlsuite"
	"github.com/engi-hq/engi/internal/port"
)

func TestPostgresIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("engi"), tcpostgres.WithUsername("engi"), tcpostgres.WithPassword("secret"),
		tcpostgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	sqlsuite.Run(t, sqlsuite.Env{
		Driver: port.DriverPostgres, CredType: pgcred.Name, New: postgres.New, Returning: true, Timestamp: "2024-05-06T07:08:09Z",
		Cred:        map[string]any{"host": host, "port": float64(p.Num()), "database": "engi", "user": "engi", "password": "secret", "sslMode": "disable"},
		Placeholder: func(n int) string { return fmt.Sprintf("$%d", n) },
		DDL: []string{
			`CREATE TABLE IF NOT EXISTS engi_people (id INT PRIMARY KEY, name VARCHAR(100) NOT NULL, age INT, note TEXT)`,
			`CREATE TABLE IF NOT EXISTS engi_types (id INT PRIMARY KEY, big BIGINT, "dec" NUMERIC(10,2), ts TIMESTAMPTZ, doc JSONB)`,
		},
	})
}
