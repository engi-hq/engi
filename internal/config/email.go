package config

import (
	"strconv"
	"time"
)

// EmailNodeConfig bounds the Send Email node and the IMAP trigger
// (nodes.email.*).
type EmailNodeConfig struct {
	// ConnectTimeout bounds establishing one SMTP or IMAP connection.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// SendTimeout bounds one run of the Send Email node (all its items).
	SendTimeout time.Duration `yaml:"sendTimeout"`
	// MaxRecipients caps to + cc + bcc of one message.
	MaxRecipients int `yaml:"maxRecipients"`
	// MaxMessageBytes rejects an outgoing message (attachments included) larger
	// than this, before anything is sent.
	MaxMessageBytes int64 `yaml:"maxMessageBytes"`
	// ImapTimeout bounds one IMAP poll (connect, search, fetch).
	ImapTimeout time.Duration `yaml:"imapTimeout"`
	// ImapMaxMessages caps the messages one poll emits; the rest follow on the
	// next polls.
	ImapMaxMessages int `yaml:"imapMaxMessages"`
	// ImapMaxMessageBytes skips an incoming message larger than this.
	ImapMaxMessageBytes int64 `yaml:"imapMaxMessageBytes"`
}

func defaultEmail() EmailNodeConfig {
	return EmailNodeConfig{
		ConnectTimeout: 15 * time.Second, SendTimeout: 2 * time.Minute, MaxRecipients: 100, MaxMessageBytes: 25 << 20,
		ImapTimeout: time.Minute, ImapMaxMessages: 50, ImapMaxMessageBytes: 10 << 20,
	}
}

func (c EmailNodeConfig) validate(errs []error) []error {
	errs = appendPositive(errs, "nodes.email.connectTimeout", int64(c.ConnectTimeout))
	errs = appendPositive(errs, "nodes.email.sendTimeout", int64(c.SendTimeout))
	errs = appendPositive(errs, "nodes.email.maxRecipients", int64(c.MaxRecipients))
	errs = appendPositive(errs, "nodes.email.maxMessageBytes", c.MaxMessageBytes)
	errs = appendPositive(errs, "nodes.email.imapTimeout", int64(c.ImapTimeout))
	errs = appendPositive(errs, "nodes.email.imapMaxMessages", int64(c.ImapMaxMessages))
	return appendPositive(errs, "nodes.email.imapMaxMessageBytes", c.ImapMaxMessageBytes)
}

func emailSettings() []setting {
	return []setting{
		{"ENGI_NODES_EMAIL_CONNECT_TIMEOUT", "nodes-email-connect-timeout", "SMTP and IMAP connect timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Email.ConnectTimeout })},
		{"ENGI_NODES_EMAIL_SEND_TIMEOUT", "nodes-email-send-timeout", "Send Email node time limit per run",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Email.SendTimeout })},
		{"ENGI_NODES_EMAIL_MAX_RECIPIENTS", "nodes-email-max-recipients", "max to+cc+bcc recipients per message",
			intSetting(func(c *Config) *int { return &c.Nodes.Email.MaxRecipients })},
		{"ENGI_NODES_EMAIL_MAX_MESSAGE_BYTES", "nodes-email-max-message-bytes", "largest outgoing message in bytes",
			func(c *Config, v string) (err error) {
				c.Nodes.Email.MaxMessageBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_NODES_EMAIL_IMAP_TIMEOUT", "nodes-email-imap-timeout", "IMAP trigger time limit per poll",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Email.ImapTimeout })},
		{"ENGI_NODES_EMAIL_IMAP_MAX_MESSAGES", "nodes-email-imap-max-messages", "messages one IMAP poll emits at most",
			intSetting(func(c *Config) *int { return &c.Nodes.Email.ImapMaxMessages })},
		{"ENGI_NODES_EMAIL_IMAP_MAX_MESSAGE_BYTES", "nodes-email-imap-max-message-bytes", "largest incoming message the IMAP trigger reads",
			func(c *Config, v string) (err error) {
				c.Nodes.Email.ImapMaxMessageBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
	}
}
