package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ChatTriggerConfig tunes the public chat endpoints of Chat triggers.
type ChatTriggerConfig struct {
	// BasePath is the URL prefix of chat endpoints and widgets.
	BasePath string `yaml:"basePath"`
	// MaxBodyBytes caps a chat request body; larger requests get 413.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
	// MaxSessionIDLength caps the length of a client-supplied session id.
	MaxSessionIDLength int `yaml:"maxSessionIdLength"`
	// ResponseTimeout bounds how long a message waits for the workflow's reply
	// before the caller gets 504. It must stay below server.writeTimeout.
	ResponseTimeout time.Duration `yaml:"responseTimeout"`
	// PreflightMaxAge is how long browsers may cache a CORS preflight answer.
	PreflightMaxAge time.Duration `yaml:"preflightMaxAge"`
}

func defaultChatTrigger() ChatTriggerConfig {
	return ChatTriggerConfig{
		BasePath: "/chat", MaxBodyBytes: 64 << 10, MaxSessionIDLength: 128,
		ResponseTimeout: 50 * time.Second, PreflightMaxAge: 10 * time.Minute,
	}
}

func (c ChatTriggerConfig) validate(errs []error, w WebhookConfig) []error {
	if len(c.BasePath) < 2 || c.BasePath[0] != '/' || c.BasePath[len(c.BasePath)-1] == '/' {
		errs = append(errs, fmt.Errorf("chat.basePath %q must start with / and not end with /", c.BasePath))
	}
	if c.BasePath == w.BasePath || c.BasePath == w.TestBasePath || c.BasePath == w.WaitBasePath {
		errs = append(errs, errors.New("chat.basePath must differ from the webhook base paths"))
	}
	errs = appendPositive(errs, "chat.maxBodyBytes", c.MaxBodyBytes)
	errs = appendPositive(errs, "chat.maxSessionIdLength", int64(c.MaxSessionIDLength))
	errs = appendPositive(errs, "chat.responseTimeout", int64(c.ResponseTimeout))
	if c.PreflightMaxAge < 0 {
		errs = append(errs, errors.New("chat.preflightMaxAge must not be negative"))
	}
	return errs
}

func chatTriggerSettings() []setting {
	return []setting{
		{"ENGI_CHAT_BASE_PATH", "chat-base-path", "URL prefix of chat endpoints and widgets",
			stringSetting(func(c *Config) *string { return &c.Trigger.Chat.BasePath })},
		{"ENGI_CHAT_MAX_BODY_BYTES", "chat-max-body-bytes", "max chat request body bytes",
			func(c *Config, v string) (err error) {
				c.Trigger.Chat.MaxBodyBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_CHAT_MAX_SESSION_ID_LENGTH", "chat-max-session-id-length", "max length of a client-supplied chat session id",
			intSetting(func(c *Config) *int { return &c.Trigger.Chat.MaxSessionIDLength })},
		{"ENGI_CHAT_RESPONSE_TIMEOUT", "chat-response-timeout", "how long a chat message waits for the workflow's reply",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Chat.ResponseTimeout })},
		{"ENGI_CHAT_PREFLIGHT_MAX_AGE", "chat-preflight-max-age", "how long browsers cache a chat CORS preflight",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Chat.PreflightMaxAge })},
	}
}
