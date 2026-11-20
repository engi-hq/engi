package config

import (
	"strings"
	"testing"
)

func TestGoogleAndAirtableDefaultsValidate(t *testing.T) {
	d := Default().Nodes
	if err := d.Google.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := d.Airtable.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleValidateRejects(t *testing.T) {
	for name, mut := range map[string]func(*GoogleNodeConfig){
		"sheetsBaseUrl": func(c *GoogleNodeConfig) { c.SheetsBaseURL = "sheets" },
		"maxRetryWait":  func(c *GoogleNodeConfig) { c.MaxRetryWait = 0 },
		"maxRetries":    func(c *GoogleNodeConfig) { c.MaxRetries = -1 },
		"maxReadRows":   func(c *GoogleNodeConfig) { c.MaxReadRows = 0 },
	} {
		c := defaultGoogle()
		mut(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nodes.google."+name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestAirtableValidateRejects(t *testing.T) {
	for name, mut := range map[string]func(*AirtableNodeConfig){
		"baseUrl":        func(c *AirtableNodeConfig) { c.BaseURL = "" },
		"maxRetryWait":   func(c *AirtableNodeConfig) { c.MaxRetryWait = 0 },
		"maxRetries":     func(c *AirtableNodeConfig) { c.MaxRetries = -1 },
		"maxListResults": func(c *AirtableNodeConfig) { c.MaxListResults = 0 },
		"pageSize":       func(c *AirtableNodeConfig) { c.PageSize = 0 },
		"batchSize":      func(c *AirtableNodeConfig) { c.BatchSize = airtableMaxBatch + 1 },
	} {
		c := defaultAirtable()
		mut(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nodes.airtable."+name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
