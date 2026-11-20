// Package googlesheets implements the Google Sheets node (engi.google.sheets)
// over the Sheets API v4 with plain HTTP: append, read, update and clear rows,
// create a tab and list tabs. It authenticates with an OAuth2 credential or a
// service account key.
package googlesheets

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/engi-hq/engi/internal/credtype/googleserviceaccount"
	"github.com/engi-hq/engi/internal/credtype/oauth2"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.google.sheets"

// Operations.
const (
	opAppend = "appendRows"
	opRead   = "readRows"
	opUpdate = "updateRows"
	opClear  = "clearRange"
	opCreate = "createSheet"
	opList   = "listSheets"
)

// Authentication choices.
const (
	authOAuth2         = "oauth2"
	authServiceAccount = "serviceAccount"
)

// Update matching and not-found behaviour.
const (
	matchRowNumber = "rowNumber"
	matchColumn    = "column"
	notFoundError  = "error"
	notFoundSkip   = "skip"
	notFoundAppend = "append"
)

// Config is what the node needs from nodes.google.*.
type Config struct {
	BaseURL     string
	MaxReadRows int
	Caller      chatkit.Caller
	// Now supplies the time for service-account tokens.
	Now func() time.Time
}

// Node is the Google Sheets node.
type Node struct{ cfg Config }

// New returns a Google Sheets node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := [][2]string{
		{opAppend, "Append rows"}, {opRead, "Read rows"}, {opUpdate, "Update rows"},
		{opClear, "Clear range"}, {opCreate, "Create sheet (tab)"}, {opList, "List sheets (tabs)"},
	}
	opts := make([]nodesdk.Option, len(ops))
	for i, o := range ops {
		opts[i] = nodekit.Opt(o[0], o[1])
	}
	tabOps := nodekit.Show("operation", opAppend, opRead, opUpdate, opClear)
	writeOps := nodekit.Show("operation", opAppend, opUpdate)
	headerOps := nodekit.Show("operation", opAppend, opRead, opUpdate)
	match := func(by string) *nodesdk.DisplayOptions {
		return &nodesdk.DisplayOptions{Show: map[string][]any{"operation": {opUpdate}, "matchBy": {by}}}
	}
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Google Sheets", Group: nodesdk.GroupAction, Icon: "table",
		Description: "Append, read, update and clear rows of a Google Sheet, create tabs and list them.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{
			{Name: oauth2.Name, Type: oauth2.Name},
			{Name: googleserviceaccount.Name, Type: googleserviceaccount.Name},
		},
		Properties: []nodesdk.Property{
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: authOAuth2, Options: []nodesdk.Option{
				nodekit.Opt(authOAuth2, "OAuth2 (engi.oauth2)"), nodekit.Opt(authServiceAccount, "Service account (engi.googleServiceAccount)")}},
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opRead, Options: opts},
			{Name: "spreadsheetId", DisplayName: "Spreadsheet", Type: nodesdk.PropString, Required: true,
				Description: "Spreadsheet ID, or the full spreadsheet URL."},
			{Name: "sheet", DisplayName: "Sheet (tab) name", Type: nodesdk.PropString, Required: true, DisplayOptions: tabOps},
			{Name: "title", DisplayName: "New sheet name", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opCreate)},
			{Name: "headerRow", DisplayName: "Header row", Type: nodesdk.PropNumber, Default: 1, DisplayOptions: headerOps,
				Description: "Row number holding the column names. Ignored by Read rows when a range is set (the range's first row is the header)."},
			{Name: "range", DisplayName: "Range", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opRead, opClear),
				Description: "A1 range inside the sheet, e.g. A1:F200. Empty reads (or clears) the whole sheet."},
			{Name: "valueRenderOption", DisplayName: "Value format", Type: nodesdk.PropOptions, Default: "FORMATTED_VALUE", DisplayOptions: nodekit.Show("operation", opRead),
				Options: []nodesdk.Option{nodekit.Opt("FORMATTED_VALUE", "As displayed"), nodekit.Opt("UNFORMATTED_VALUE", "Raw values"), nodekit.Opt("FORMULA", "Formulas")}},
			{Name: "columns", DisplayName: "Columns (JSON)", Type: nodesdk.PropJSON, DisplayOptions: writeOps,
				Description: "Object of column name to value. Empty uses the input item's JSON."},
			{Name: "extraColumns", DisplayName: "Columns missing from the header", Type: nodesdk.PropOptions, Default: "error", DisplayOptions: writeOps,
				Options: []nodesdk.Option{nodekit.Opt("error", "Fail"), nodekit.Opt("ignore", "Ignore")}},
			{Name: "valueInputOption", DisplayName: "Value input", Type: nodesdk.PropOptions, Default: "USER_ENTERED", DisplayOptions: writeOps,
				Options: []nodesdk.Option{nodekit.Opt("USER_ENTERED", "Parsed like typing in the UI"), nodekit.Opt("RAW", "Stored as given")}},
			{Name: "matchBy", DisplayName: "Find the row by", Type: nodesdk.PropOptions, Default: matchColumn, DisplayOptions: nodekit.Show("operation", opUpdate),
				Options: []nodesdk.Option{nodekit.Opt(matchColumn, "Value in a key column"), nodekit.Opt(matchRowNumber, "Row number")}},
			{Name: "rowNumber", DisplayName: "Row number", Type: nodesdk.PropNumber, Required: true, DisplayOptions: match(matchRowNumber)},
			{Name: "matchColumn", DisplayName: "Key column", Type: nodesdk.PropString, Required: true, DisplayOptions: match(matchColumn),
				Description: "Header name of the column to match on."},
			{Name: "matchValue", DisplayName: "Key value", Type: nodesdk.PropString, DisplayOptions: match(matchColumn),
				Description: "Value to find. Empty uses the item's value of the key column."},
			{Name: "ifNotFound", DisplayName: "If no row matches", Type: nodesdk.PropOptions, Default: notFoundError, DisplayOptions: match(matchColumn),
				Options: []nodesdk.Option{nodekit.Opt(notFoundError, "Fail"), nodekit.Opt(notFoundSkip, "Skip the item"), nodekit.Opt(notFoundAppend, "Append as a new row")}},
			{Name: "rowNumberField", DisplayName: "Row number field", Type: nodesdk.PropString, Default: "rowNumber", DisplayOptions: headerOps,
				Description: "Output field that carries the sheet row number. Empty omits it."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	c, err := n.newClient(ctx, in)
	if err != nil {
		return nodesdk.Output{}, err
	}
	r := &runner{c: c, in: in, maxRows: n.cfg.MaxReadRows, state: map[string]*sheetState{}}
	var out []nodesdk.Item
	if op == opAppend {
		out, err = r.appendAll(ctx, items)
		if err != nil {
			return nodesdk.Output{}, err
		}
		return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
	}
	for i := range items {
		res, err := r.run(ctx, op, i, items[i])
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemFailure(i, err)
		}
		for _, row := range res {
			out = append(out, nodekit.Paired(nodesdk.Item{JSON: row}, i))
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// newClient resolves the credential and the way it authenticates requests.
func (n *Node) newClient(ctx context.Context, in nodesdk.Input) (*client, error) {
	mode, err := nodesdk.ParamString(ctx, in, "authentication", 0)
	if err != nil {
		return nil, err
	}
	c := &client{cfg: n.cfg, http: in.HTTP()}
	switch mode {
	case authOAuth2:
		cred, err := in.Credential(ctx, oauth2.Name)
		if err != nil {
			return nil, fmt.Errorf("loading credential %q: %w", oauth2.Name, err)
		}
		c.auth = cred.Authenticate
		c.secrets = []string{chatkit.Secret(cred, oauth2.FieldAccess)}
	case authServiceAccount:
		cred, err := in.Credential(ctx, googleserviceaccount.Name)
		if err != nil {
			return nil, fmt.Errorf("loading credential %q: %w", googleserviceaccount.Name, err)
		}
		token, err := c.serviceAccountToken(ctx, cred)
		if err != nil {
			return nil, err
		}
		c.auth = func(req *http.Request) error { req.Header.Set("Authorization", "Bearer "+token); return nil }
		c.secrets = []string{token, chatkit.Secret(cred, googleserviceaccount.FieldPrivateKey)}
	default:
		return nil, fmt.Errorf("unknown authentication %q", mode)
	}
	return c, nil
}
