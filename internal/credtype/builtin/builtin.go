// Package builtin lists the credential types shipped with Engi.
package builtin

import (
	"fmt"

	"github.com/engi-hq/engi/internal/credtype/airtable"
	"github.com/engi-hq/engi/internal/credtype/anthropic"
	"github.com/engi-hq/engi/internal/credtype/azureopenai"
	"github.com/engi-hq/engi/internal/credtype/bearertoken"
	"github.com/engi-hq/engi/internal/credtype/googleserviceaccount"
	"github.com/engi-hq/engi/internal/credtype/httpbasic"
	"github.com/engi-hq/engi/internal/credtype/httpheader"
	credimap "github.com/engi-hq/engi/internal/credtype/imap"
	"github.com/engi-hq/engi/internal/credtype/kafka"
	credmcp "github.com/engi-hq/engi/internal/credtype/mcp"
	"github.com/engi-hq/engi/internal/credtype/mongodb"
	"github.com/engi-hq/engi/internal/credtype/mysql"
	"github.com/engi-hq/engi/internal/credtype/oauth2"
	"github.com/engi-hq/engi/internal/credtype/ollama"
	"github.com/engi-hq/engi/internal/credtype/openai"
	"github.com/engi-hq/engi/internal/credtype/postgres"
	"github.com/engi-hq/engi/internal/credtype/querykey"
	"github.com/engi-hq/engi/internal/credtype/redis"
	"github.com/engi-hq/engi/internal/credtype/registry"
	creds3 "github.com/engi-hq/engi/internal/credtype/s3"
	"github.com/engi-hq/engi/internal/credtype/slack"
	credsmtp "github.com/engi-hq/engi/internal/credtype/smtp"
	"github.com/engi-hq/engi/internal/credtype/teamswebhook"
	"github.com/engi-hq/engi/internal/credtype/telegram"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Types returns every built-in credential type.
func Types() []nodesdk.CredentialType {
	return []nodesdk.CredentialType{
		httpbasic.Type(), httpheader.Type(), bearertoken.Type(), querykey.Type(), oauth2.Type(), postgres.Type(), mysql.Type(), redis.Type(), mongodb.Type(), kafka.Type(), creds3.Type(), credsmtp.Type(), credimap.Type(), slack.Type(), telegram.Type(), teamswebhook.Type(),
		anthropic.Type(), openai.Type(), azureopenai.Type(), ollama.Type(), credmcp.Type(),
		airtable.Type(), googleserviceaccount.Type(),
	}
}

// Registry returns a registry holding all built-in types.
func Registry() (*registry.Registry, error) {
	r := registry.New()
	for _, t := range Types() {
		if err := r.Register(t); err != nil {
			return nil, fmt.Errorf("registering built-in credential types: %w", err)
		}
	}
	return r, nil
}
