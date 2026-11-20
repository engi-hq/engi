package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ChatNodeConfig tunes the Slack, Telegram and Microsoft Teams nodes. The base
// URLs exist so the nodes can target a local Bot API server or a test server.
type ChatNodeConfig struct {
	// SlackBaseURL is the Slack Web API root.
	SlackBaseURL string `yaml:"slackBaseUrl"`
	// TelegramBaseURL is the Telegram Bot API root (the bot token is added per request).
	TelegramBaseURL string `yaml:"telegramBaseUrl"`
	// GraphBaseURL is the Microsoft Graph root.
	GraphBaseURL string `yaml:"graphBaseUrl"`
	// MaxRetryWait caps how long one rate-limit (HTTP 429) wait may last. A
	// longer Retry-After fails the call instead of blocking the worker.
	MaxRetryWait time.Duration `yaml:"maxRetryWait"`
	// MaxRetries is how many times a rate-limited call is retried.
	MaxRetries int `yaml:"maxRetries"`
	// MaxListResults caps the channels one Slack "list channels" run returns.
	MaxListResults int `yaml:"maxListResults"`
	// PageSize is the page size requested from paginated list endpoints.
	PageSize int `yaml:"pageSize"`
}

func defaultChat() ChatNodeConfig {
	return ChatNodeConfig{
		SlackBaseURL: "https://slack.com/api", TelegramBaseURL: "https://api.telegram.org", GraphBaseURL: "https://graph.microsoft.com/v1.0",
		MaxRetryWait: 30 * time.Second, MaxRetries: 2, MaxListResults: 1000, PageSize: 200,
	}
}

// Validate reports every problem with the chat node settings.
func (c ChatNodeConfig) Validate() error {
	var errs []error
	for name, v := range map[string]string{"nodes.chat.slackBaseUrl": c.SlackBaseURL, "nodes.chat.telegramBaseUrl": c.TelegramBaseURL, "nodes.chat.graphBaseUrl": c.GraphBaseURL} {
		if u, err := url.Parse(v); err != nil || !u.IsAbs() || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s %q is not an absolute URL", name, v))
		}
	}
	errs = appendPositive(errs, "nodes.chat.maxRetryWait", int64(c.MaxRetryWait))
	errs = appendPositive(errs, "nodes.chat.maxListResults", int64(c.MaxListResults))
	errs = appendPositive(errs, "nodes.chat.pageSize", int64(c.PageSize))
	if c.MaxRetries < 0 {
		errs = append(errs, errors.New("nodes.chat.maxRetries must not be negative"))
	}
	return errors.Join(errs...)
}

func chatSettings() []setting {
	return []setting{
		{"ENGI_NODES_CHAT_SLACK_BASE_URL", "nodes-chat-slack-base-url", "Slack Web API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.Chat.SlackBaseURL })},
		{"ENGI_NODES_CHAT_TELEGRAM_BASE_URL", "nodes-chat-telegram-base-url", "Telegram Bot API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.Chat.TelegramBaseURL })},
		{"ENGI_NODES_CHAT_GRAPH_BASE_URL", "nodes-chat-graph-base-url", "Microsoft Graph base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.Chat.GraphBaseURL })},
		{"ENGI_NODES_CHAT_MAX_RETRY_WAIT", "nodes-chat-max-retry-wait", "longest rate-limit wait the chat nodes accept",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Chat.MaxRetryWait })},
		{"ENGI_NODES_CHAT_MAX_RETRIES", "nodes-chat-max-retries", "retries of a rate-limited chat call",
			intSetting(func(c *Config) *int { return &c.Nodes.Chat.MaxRetries })},
		{"ENGI_NODES_CHAT_MAX_LIST_RESULTS", "nodes-chat-max-list-results", "max channels one Slack list run returns",
			intSetting(func(c *Config) *int { return &c.Nodes.Chat.MaxListResults })},
		{"ENGI_NODES_CHAT_PAGE_SIZE", "nodes-chat-page-size", "page size of paginated chat list calls",
			intSetting(func(c *Config) *int { return &c.Nodes.Chat.PageSize })},
	}
}
