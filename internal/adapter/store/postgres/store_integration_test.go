//go:build integration

package postgres_test

import (
	"testing"

	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	"github.com/engi-hq/engi/internal/adapter/store/postgres"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
)

func TestStoreContract(t *testing.T) {
	porttest.RunStoreSuite(t, func(t *testing.T, clk port.Clock) porttest.Stores {
		t.Helper()
		env := pgtest.New(t)
		s := postgres.New(env.DB, env.Store, clk)
		return porttest.Stores{
			Workflows: s.Workflows(), Versions: s.Workflows(),
			Executions: s.Executions(), States: s.Executions(),
			Credentials: s.Credentials(), Tags: s.Tags(), Variables: s.Variables(), Tx: s,
		}
	})
}

func TestAuthStoreContract(t *testing.T) {
	porttest.RunAuthSuite(t, func(t *testing.T, clk port.Clock) porttest.AuthStores {
		t.Helper()
		env := pgtest.New(t)
		s := postgres.New(env.DB, env.Store, clk)
		return porttest.AuthStores{Users: s.Users(), Sessions: s.Sessions(), APIKeys: s.APIKeys()}
	})
}

func TestChatMemoryContract(t *testing.T) {
	porttest.RunChatMemorySuite(t, func(t *testing.T, clk port.Clock, mem config.AIMemoryConfig) port.ChatMemoryStore {
		t.Helper()
		env := pgtest.New(t)
		return postgres.New(env.DB, env.Store, clk).ChatMemory(mem)
	})
}

func TestTestWebhookContract(t *testing.T) {
	porttest.RunTestWebhookSuite(t, func(t *testing.T, clk port.Clock) porttest.TestWebhookEnv {
		t.Helper()
		env := pgtest.New(t)
		s := postgres.New(env.DB, env.Store, clk)
		return porttest.TestWebhookEnv{Tests: s.TestWebhooks(), Workflows: s.Workflows()}
	})
}
