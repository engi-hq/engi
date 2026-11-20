package googlesheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// sheetState caches what one run learns about a tab so many items cost one
// header read and one key-column read, not one per item.
type sheetState struct {
	header  []string
	keyCols map[string][]string // header name -> cell values, index 0 = row 1
}

type runner struct {
	c       *client
	in      nodesdk.Input
	maxRows int
	state   map[string]*sheetState
}

// run executes a per-item operation and returns the output rows for item i.
func (r *runner) run(ctx context.Context, op string, i int, item nodesdk.Item) ([]map[string]any, error) {
	id, err := r.spreadsheetID(ctx, i)
	if err != nil {
		return nil, err
	}
	switch op {
	case opRead:
		return r.read(ctx, id, i)
	case opUpdate:
		return r.update(ctx, id, i, item)
	case opClear:
		return r.clear(ctx, id, i)
	case opCreate:
		return r.createSheet(ctx, id, i)
	case opList:
		return r.listSheets(ctx, id)
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

var spreadsheetURL = regexp.MustCompile(`/spreadsheets/d/([^/?#]+)`)

func (r *runner) spreadsheetID(ctx context.Context, i int) (string, error) {
	v, err := nodesdk.ParamString(ctx, r.in, "spreadsheetId", i)
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	if m := spreadsheetURL.FindStringSubmatch(v); m != nil {
		v = m[1]
	}
	if v == "" {
		return "", errors.New("spreadsheetId must not be empty")
	}
	return v, nil
}

func (r *runner) sheetName(ctx context.Context, i int) (string, error) {
	v, err := nodesdk.ParamString(ctx, r.in, "sheet", i)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return "", errors.New("sheet must not be empty")
	}
	return v, nil
}

// quoteSheet quotes a tab name for A1 notation.
func quoteSheet(name string) string { return "'" + strings.ReplaceAll(name, "'", "''") + "'" }

func valuesPath(id, rng string) string {
	return "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(rng)
}

func (r *runner) headerRow(ctx context.Context, i int) (int, error) {
	n, err := nodesdk.ParamInt(ctx, r.in, "headerRow", i)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		n = 1
	}
	if n < 1 {
		return 0, errors.New("headerRow must be at least 1")
	}
	return n, nil
}

func (r *runner) rowNumberField(ctx context.Context, i int) (string, error) {
	v, err := r.in.Param(ctx, "rowNumberField", i)
	if err != nil {
		return "", fmt.Errorf("reading parameter %q: %w", "rowNumberField", err)
	}
	if v == nil {
		return "rowNumber", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("rowNumberField must be a string")
	}
	return s, nil
}

// cellText renders a returned cell for header and key comparisons.
func cellText(v any) string { return strings.TrimSpace(nodekit.ToString(v)) }

// rowStart parses the first row number out of an A1 range such as
// "Sheet1!A5:C9". It returns 1 when the range names whole columns.
func rowStart(rng string) int {
	if k := strings.LastIndex(rng, "!"); k >= 0 {
		rng = rng[k+1:]
	}
	first, _, _ := strings.Cut(rng, ":")
	digits := strings.TrimLeft(first, "$ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
	n, err := strconv.Atoi(strings.TrimPrefix(digits, "$"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// columnLetters converts a zero-based column index to A1 letters (0 = A, 26 = AA).
func columnLetters(idx int) string {
	s := ""
	for n := idx + 1; n > 0; n = (n - 1) / 26 {
		s = string(rune('A'+(n-1)%26)) + s
	}
	return s
}

func rowsOf(body map[string]any) [][]any {
	raw, _ := body["values"].([]any)
	out := make([][]any, len(raw))
	for k, r := range raw {
		out[k], _ = r.([]any)
	}
	return out
}

func (r *runner) read(ctx context.Context, id string, i int) ([]map[string]any, error) {
	sheet, err := r.sheetName(ctx, i)
	if err != nil {
		return nil, err
	}
	rng, err := nodesdk.ParamString(ctx, r.in, "range", i)
	if err != nil {
		return nil, err
	}
	render, err := nodesdk.ParamString(ctx, r.in, "valueRenderOption", i)
	if err != nil {
		return nil, err
	}
	field, err := r.rowNumberField(ctx, i)
	if err != nil {
		return nil, err
	}
	a1 := quoteSheet(sheet)
	if rng = strings.TrimSpace(rng); rng != "" {
		a1 += "!" + rng
	} else {
		h, err := r.headerRow(ctx, i)
		if err != nil {
			return nil, err
		}
		// Bound the request: the header plus at most maxRows data rows.
		a1 += fmt.Sprintf("!%d:%d", h, h+r.maxRows)
	}
	q := url.Values{"majorDimension": {"ROWS"}}
	if render != "" {
		q.Set("valueRenderOption", render)
	}
	body, err := r.c.call(ctx, http.MethodGet, valuesPath(id, a1), q, nil)
	if err != nil {
		return nil, err
	}
	rows := rowsOf(body)
	if len(rows) == 0 {
		return nil, nil
	}
	rangeStr, _ := body["range"].(string)
	first := rowStart(rangeStr)
	header := make([]string, len(rows[0]))
	for k, h := range rows[0] {
		header[k] = cellText(h)
	}
	var out []map[string]any
	for k, row := range rows[1:] {
		if len(out) >= r.maxRows {
			break
		}
		if emptyRow(row) {
			continue
		}
		obj := map[string]any{}
		for col, name := range header {
			if name == "" {
				continue
			}
			if _, dup := obj[name]; dup {
				continue
			}
			if col < len(row) {
				obj[name] = row[col]
			} else {
				obj[name] = ""
			}
		}
		if field != "" {
			obj[field] = float64(first + 1 + k)
		}
		out = append(out, obj)
	}
	return out, nil
}

func emptyRow(row []any) bool {
	for _, c := range row {
		if cellText(c) != "" {
			return false
		}
	}
	return true
}

// header returns (and caches) the header row of a tab.
func (r *runner) header(ctx context.Context, id, sheet string, headerRow int) (*sheetState, error) {
	key := id + "\x00" + sheet + "\x00" + strconv.Itoa(headerRow)
	if st, ok := r.state[key]; ok {
		return st, nil
	}
	rng := fmt.Sprintf("%s!%d:%d", quoteSheet(sheet), headerRow, headerRow)
	body, err := r.c.call(ctx, http.MethodGet, valuesPath(id, rng), url.Values{"majorDimension": {"ROWS"}}, nil)
	if err != nil {
		return nil, err
	}
	rows := rowsOf(body)
	if len(rows) == 0 || emptyRow(rows[0]) {
		return nil, fmt.Errorf("header row %d of sheet %q is empty", headerRow, sheet)
	}
	st := &sheetState{header: make([]string, len(rows[0])), keyCols: map[string][]string{}}
	for k, h := range rows[0] {
		st.header[k] = cellText(h)
	}
	r.state[key] = st
	return st, nil
}

// keyColumn returns (and caches) the cell texts of the named header column.
func (r *runner) keyColumn(ctx context.Context, id, sheet string, st *sheetState, name string) ([]string, error) {
	if col, ok := st.keyCols[name]; ok {
		return col, nil
	}
	idx := indexOf(st.header, name)
	if idx < 0 {
		return nil, fmt.Errorf("key column %q is not in the header row", name)
	}
	letters := columnLetters(idx)
	body, err := r.c.call(ctx, http.MethodGet, valuesPath(id, fmt.Sprintf("%s!%s:%s", quoteSheet(sheet), letters, letters)),
		url.Values{"majorDimension": {"COLUMNS"}}, nil)
	if err != nil {
		return nil, err
	}
	var col []string
	if cols := rowsOf(body); len(cols) > 0 {
		col = make([]string, len(cols[0]))
		for k, v := range cols[0] {
			col[k] = cellText(v)
		}
	}
	st.keyCols[name] = col
	return col, nil
}

func indexOf(list []string, s string) int {
	for k, v := range list {
		if v == s {
			return k
		}
	}
	return -1
}

// objectParam reads an object given as a map or as JSON text. Empty is nil.
func objectParam(ctx context.Context, in nodesdk.Input, name string, i int) (map[string]any, error) {
	v, err := in.Param(ctx, name, i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return t, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil, fmt.Errorf("%s is not a JSON object: %w", name, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("%s must be an object, got %s", name, nodekit.TypeName(v))
}

// rowData is the column map to write for item i: the columns parameter, or
// the item's JSON, minus the row-number field.
func (r *runner) rowData(ctx context.Context, i int, item nodesdk.Item) (map[string]any, error) {
	m, err := objectParam(ctx, r.in, "columns", i)
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = item.JSON
	}
	field, err := r.rowNumberField(ctx, i)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if field == "" || k != field {
			out[k] = v
		}
	}
	return out, nil
}

// checkColumns fails (or not, per extraColumns) when data names columns the header lacks.
func (r *runner) checkColumns(ctx context.Context, i int, st *sheetState, data map[string]any) error {
	mode, err := nodesdk.ParamString(ctx, r.in, "extraColumns", i)
	if err != nil {
		return err
	}
	if mode == "ignore" {
		return nil
	}
	var missing []string
	for k := range data {
		if indexOf(st.header, k) < 0 {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("columns %v are not in the header row (set extraColumns to ignore them)", missing)
	}
	return nil
}

// cellValue converts an item value into something the Sheets API accepts.
func cellValue(v any) any {
	switch t := v.(type) {
	case nil:
		return ""
	case string, bool, float64:
		return t
	}
	return nodekit.ToString(v)
}

// appendAll appends one row per input item in a single request.
func (r *runner) appendAll(ctx context.Context, items []nodesdk.Item) ([]nodesdk.Item, error) {
	id, err := r.spreadsheetID(ctx, 0)
	if err != nil {
		return nil, err
	}
	sheet, err := r.sheetName(ctx, 0)
	if err != nil {
		return nil, err
	}
	hr, err := r.headerRow(ctx, 0)
	if err != nil {
		return nil, err
	}
	input, err := nodesdk.ParamString(ctx, r.in, "valueInputOption", 0)
	if err != nil {
		return nil, err
	}
	st, err := r.header(ctx, id, sheet, hr)
	if err != nil {
		return nil, nodekit.ItemFailure(0, err)
	}
	values := make([][]any, len(items))
	datas := make([]map[string]any, len(items))
	for i := range items {
		data, err := r.rowData(ctx, i, items[i])
		if err != nil {
			return nil, nodekit.ItemFailure(i, err)
		}
		if err := r.checkColumns(ctx, i, st, data); err != nil {
			return nil, nodekit.ItemFailure(i, err)
		}
		row := make([]any, len(st.header))
		for col, name := range st.header {
			row[col] = ""
			if v, ok := data[name]; ok && name != "" {
				row[col] = cellValue(v)
			}
		}
		values[i], datas[i] = row, data
	}
	rows, err := r.appendRows(ctx, id, sheet, hr, input, values)
	if err != nil {
		return nil, nodekit.ItemFailure(0, err)
	}
	field, err := r.rowNumberField(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make([]nodesdk.Item, len(items))
	for i := range items {
		obj := make(map[string]any, len(datas[i])+1)
		for k, v := range datas[i] {
			obj[k] = v
		}
		if field != "" && rows > 0 {
			obj[field] = float64(rows + i)
		}
		out[i] = nodekit.Paired(nodesdk.Item{JSON: obj}, i)
	}
	return out, nil
}

// appendRows posts the rows below the table and returns the number of the
// first appended row (0 when Google does not report it).
func (r *runner) appendRows(ctx context.Context, id, sheet string, headerRow int, input string, values [][]any) (int, error) {
	if input == "" {
		input = "USER_ENTERED"
	}
	q := url.Values{"valueInputOption": {input}, "insertDataOption": {"INSERT_ROWS"}}
	rng := fmt.Sprintf("%s!A%d", quoteSheet(sheet), headerRow)
	body, err := r.c.call(ctx, http.MethodPost, valuesPath(id, rng)+":append", q,
		map[string]any{"majorDimension": "ROWS", "values": values})
	if err != nil {
		return 0, err
	}
	upd, _ := body["updates"].(map[string]any)
	updated, _ := upd["updatedRange"].(string)
	if updated == "" {
		return 0, nil
	}
	return rowStart(updated), nil
}

func (r *runner) update(ctx context.Context, id string, i int, item nodesdk.Item) ([]map[string]any, error) {
	sheet, err := r.sheetName(ctx, i)
	if err != nil {
		return nil, err
	}
	hr, err := r.headerRow(ctx, i)
	if err != nil {
		return nil, err
	}
	by, err := nodesdk.ParamString(ctx, r.in, "matchBy", i)
	if err != nil {
		return nil, err
	}
	input, err := nodesdk.ParamString(ctx, r.in, "valueInputOption", i)
	if err != nil {
		return nil, err
	}
	field, err := r.rowNumberField(ctx, i)
	if err != nil {
		return nil, err
	}
	st, err := r.header(ctx, id, sheet, hr)
	if err != nil {
		return nil, err
	}
	data, err := r.rowData(ctx, i, item)
	if err != nil {
		return nil, err
	}
	if err := r.checkColumns(ctx, i, st, data); err != nil {
		return nil, err
	}
	row, found, err := r.findRow(ctx, id, sheet, st, hr, by, i, data)
	if err != nil {
		return nil, err
	}
	if !found {
		mode, err := nodesdk.ParamString(ctx, r.in, "ifNotFound", i)
		if err != nil {
			return nil, err
		}
		switch mode {
		case notFoundSkip:
			return []map[string]any{copyMap(data)}, nil
		case notFoundAppend:
			return r.appendMissing(ctx, id, sheet, hr, input, st, by, i, data, field)
		}
		return nil, fmt.Errorf("no row matches %q", r.matchLabel(ctx, i, data))
	}
	var batch []map[string]any
	for col, name := range st.header {
		if v, ok := data[name]; ok && name != "" {
			batch = append(batch, map[string]any{
				"range":  fmt.Sprintf("%s!%s%d", quoteSheet(sheet), columnLetters(col), row),
				"values": [][]any{{cellValue(v)}},
			})
		}
	}
	if len(batch) > 0 {
		if input == "" {
			input = "USER_ENTERED"
		}
		if _, err := r.c.call(ctx, http.MethodPost, "/spreadsheets/"+url.PathEscape(id)+"/values:batchUpdate", nil,
			map[string]any{"valueInputOption": input, "data": batch}); err != nil {
			return nil, err
		}
	}
	res := copyMap(data)
	if field != "" {
		res[field] = float64(row)
	}
	return []map[string]any{res}, nil
}

func (r *runner) matchLabel(ctx context.Context, i int, data map[string]any) string {
	col, _ := nodesdk.ParamString(ctx, r.in, "matchColumn", i)
	return col + "=" + r.matchValue(ctx, i, col, data)
}

func (r *runner) matchValue(ctx context.Context, i int, col string, data map[string]any) string {
	if v, _ := nodesdk.ParamString(ctx, r.in, "matchValue", i); v != "" {
		return v
	}
	return cellText(data[col])
}

// findRow resolves the sheet row an item updates.
func (r *runner) findRow(ctx context.Context, id, sheet string, st *sheetState, headerRow int, by string, i int, data map[string]any) (row int, found bool, err error) {
	if by == matchRowNumber {
		n, err := nodesdk.ParamInt(ctx, r.in, "rowNumber", i)
		if err != nil {
			return 0, false, err
		}
		if n <= headerRow {
			return 0, false, fmt.Errorf("rowNumber must be greater than the header row (%d)", headerRow)
		}
		return n, true, nil
	}
	col, err := nodesdk.ParamString(ctx, r.in, "matchColumn", i)
	if err != nil {
		return 0, false, err
	}
	if strings.TrimSpace(col) == "" {
		return 0, false, errors.New("matchColumn must not be empty")
	}
	want := r.matchValue(ctx, i, col, data)
	if want == "" {
		return 0, false, fmt.Errorf("no value to match in column %q", col)
	}
	cells, err := r.keyColumn(ctx, id, sheet, st, col)
	if err != nil {
		return 0, false, err
	}
	for k := headerRow; k < len(cells); k++ { // cells[k] is row k+1; data starts below the header
		if cells[k] == want {
			return k + 1, true, nil
		}
	}
	return 0, false, nil
}

// appendMissing appends the item as a new row and remembers its key so later
// items of the run match it.
func (r *runner) appendMissing(ctx context.Context, id, sheet string, hr int, input string, st *sheetState, by string, i int, data map[string]any, field string) ([]map[string]any, error) {
	if by != matchColumn {
		return nil, errors.New("appending a missing row needs a key column match")
	}
	row := make([]any, len(st.header))
	for col, name := range st.header {
		row[col] = ""
		if v, ok := data[name]; ok && name != "" {
			row[col] = cellValue(v)
		}
	}
	first, err := r.appendRows(ctx, id, sheet, hr, input, [][]any{row})
	if err != nil {
		return nil, err
	}
	if col, _ := nodesdk.ParamString(ctx, r.in, "matchColumn", i); first > 0 {
		cells := st.keyCols[col]
		for len(cells) < first-1 {
			cells = append(cells, "")
		}
		st.keyCols[col] = append(cells[:first-1], r.matchValue(ctx, i, col, data))
	}
	res := copyMap(data)
	if field != "" && first > 0 {
		res[field] = float64(first)
	}
	return []map[string]any{res}, nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r *runner) clear(ctx context.Context, id string, i int) ([]map[string]any, error) {
	sheet, err := r.sheetName(ctx, i)
	if err != nil {
		return nil, err
	}
	rng, err := nodesdk.ParamString(ctx, r.in, "range", i)
	if err != nil {
		return nil, err
	}
	a1 := quoteSheet(sheet)
	if rng = strings.TrimSpace(rng); rng != "" {
		a1 += "!" + rng
	}
	body, err := r.c.call(ctx, http.MethodPost, valuesPath(id, a1)+":clear", nil, map[string]any{})
	if err != nil {
		return nil, err
	}
	// Cached header and key columns are stale once cells are cleared.
	r.state = map[string]*sheetState{}
	return []map[string]any{{"spreadsheetId": id, "clearedRange": body["clearedRange"]}}, nil
}

func (r *runner) createSheet(ctx context.Context, id string, i int) ([]map[string]any, error) {
	title, err := nodesdk.ParamString(ctx, r.in, "title", i)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("title must not be empty")
	}
	body, err := r.c.call(ctx, http.MethodPost, "/spreadsheets/"+url.PathEscape(id)+":batchUpdate", nil,
		map[string]any{"requests": []any{map[string]any{"addSheet": map[string]any{"properties": map[string]any{"title": title}}}}})
	if err != nil {
		return nil, err
	}
	replies, _ := body["replies"].([]any)
	if len(replies) == 0 {
		return nil, errors.New("google sheets: response lacks the new sheet")
	}
	first, _ := replies[0].(map[string]any)
	add, _ := first["addSheet"].(map[string]any)
	props, _ := add["properties"].(map[string]any)
	if props == nil {
		return nil, errors.New("google sheets: response lacks the new sheet's properties")
	}
	return []map[string]any{props}, nil
}

func (r *runner) listSheets(ctx context.Context, id string) ([]map[string]any, error) {
	body, err := r.c.call(ctx, http.MethodGet, "/spreadsheets/"+url.PathEscape(id), url.Values{"fields": {"sheets.properties"}}, nil)
	if err != nil {
		return nil, err
	}
	sheets, _ := body["sheets"].([]any)
	out := make([]map[string]any, 0, len(sheets))
	for _, s := range sheets {
		m, _ := s.(map[string]any)
		if p, ok := m["properties"].(map[string]any); ok {
			out = append(out, p)
		}
	}
	return out, nil
}
