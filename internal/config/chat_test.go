package config

import (
	"strings"
	"testing"
)

func TestChatDefaultsValidate(t *testing.T) {
	if err := Default().Nodes.Chat.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestChatValidateRejects(t *testing.T) {
	for name, mut := range map[string]func(*ChatNodeConfig){
		"slackBaseUrl":    func(c *ChatNodeConfig) { c.SlackBaseURL = "slack" },
		"maxRetryWait":    func(c *ChatNodeConfig) { c.MaxRetryWait = 0 },
		"maxRetries":      func(c *ChatNodeConfig) { c.MaxRetries = -1 },
		"maxListResults":  func(c *ChatNodeConfig) { c.MaxListResults = 0 },
		"pageSize":        func(c *ChatNodeConfig) { c.PageSize = 0 },
		"telegramBaseUrl": func(c *ChatNodeConfig) { c.TelegramBaseURL = "" },
	} {
		c := defaultChat()
		mut(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nodes.chat."+name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestAIDefaultsValidate(t *testing.T) {
	if err := Default().Nodes.AI.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAIValidateRejects(t *testing.T) {
	for name, mut := range map[string]func(*AINodeConfig){
		"anthropicBaseUrl":      func(c *AINodeConfig) { c.AnthropicBaseURL = "api" },
		"openaiBaseUrl":         func(c *AINodeConfig) { c.OpenAIBaseURL = "" },
		"ollamaBaseUrl":         func(c *AINodeConfig) { c.OllamaBaseURL = "localhost" },
		"anthropicVersion":      func(c *AINodeConfig) { c.AnthropicVersion = " " },
		"azureApiVersion":       func(c *AINodeConfig) { c.AzureAPIVersion = "" },
		"anthropicDefaultModel": func(c *AINodeConfig) { c.AnthropicDefaultModel = "" },
		"defaultMaxTokens":      func(c *AINodeConfig) { c.DefaultMaxTokens = 0 },
		"requestTimeout":        func(c *AINodeConfig) { c.RequestTimeout = 0 },
	} {
		c := defaultAI()
		mut(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nodes.ai."+name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
