package sqlnode

import (
	"reflect"
	"strings"
	"testing"
)

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		d       Dialect
		in      string
		want    string
		wantErr bool
	}{
		{Postgres, "users", `"users"`, false},
		{Postgres, `we"ird`, `"we""ird"`, false},
		{Postgres, `a"; DROP TABLE x; --`, `"a""; DROP TABLE x; --"`, false},
		{MySQL, "users", "`users`", false},
		{MySQL, "we`ird", "`we``ird`", false},
		{MySQL, `a"b`, "`a\"b`", false},
		{Postgres, "", "", true},
		{Postgres, "a\x00b", "", true},
		{Postgres, strings.Repeat("x", maxIdentSize+1), "", true},
	}
	for _, tt := range tests {
		got, err := tt.d.QuoteIdent(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("%s QuoteIdent(%q) = %q, %v; want %q (err %v)", tt.d.Name, tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestSelect(t *testing.T) {
	tbl := Table{Schema: "public", Name: "users"}
	tests := []struct {
		name  string
		d     Dialect
		build func(d Dialect) (Statement, error)
		sql   string
		args  []any
	}{
		{"pg all columns", Postgres, func(d Dialect) (Statement, error) { return d.Select(tbl, nil, Where{}, Sort{}, 0) },
			`SELECT * FROM "public"."users"`, nil},
		{"pg full", Postgres, func(d Dialect) (Statement, error) {
			return d.Select(tbl, []string{"id", "na me"}, Where{Conditions: []Condition{
				{"age", OpGreaterEq, 18.0}, {"name", OpLike, "a%"}, {"deleted_at", OpIsNull, nil}}}, Sort{"id", true}, 10)
		}, `SELECT "id", "na me" FROM "public"."users" WHERE "age" >= $1 AND "name" LIKE $2 AND "deleted_at" IS NULL ORDER BY "id" DESC LIMIT 10`,
			[]any{int64(18), "a%"}},
		{"mysql or", MySQL, func(d Dialect) (Statement, error) {
			return d.Select(Table{Name: "t"}, nil, Where{Combinator: CombineOr, Conditions: []Condition{
				{"a", OpEqual, "x"}, {"b", OpNotEqual, nil}, {"c", OpILike, "y"}}}, Sort{}, 0)
		}, "SELECT * FROM `t` WHERE `a` = ? OR `b` IS NOT NULL OR `c` LIKE ?", []any{"x", "y"}},
		{"in list", Postgres, func(d Dialect) (Statement, error) {
			return d.Select(Table{Name: "t"}, nil, Where{Conditions: []Condition{{"id", OpIn, "1, 2,3"}, {"s", OpNotIn, []any{"a"}}}}, Sort{}, 0)
		}, `SELECT * FROM "t" WHERE "id" IN ($1, $2, $3) AND "s" NOT IN ($4)`, []any{"1", "2", "3", "a"}},
		{"empty in matches nothing", MySQL, func(d Dialect) (Statement, error) {
			return d.Select(Table{Name: "t"}, nil, Where{Conditions: []Condition{{"id", OpIn, []any{}}, {"x", OpNotIn, ""}}}, Sort{}, 0)
		}, "SELECT * FROM `t` WHERE 1 = 0 AND 1 = 1", nil},
		{"pg ilike", Postgres, func(d Dialect) (Statement, error) {
			return d.Select(Table{Name: "t"}, nil, Where{Conditions: []Condition{{"n", OpILike, "%x"}}}, Sort{}, 0)
		}, `SELECT * FROM "t" WHERE "n" ILIKE $1`, []any{"%x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := tt.build(tt.d)
			if err != nil {
				t.Fatal(err)
			}
			if st.SQL != tt.sql || !reflect.DeepEqual(st.Args, tt.args) || !st.ReturnsRows {
				t.Errorf("got %q %#v\nwant %q %#v", st.SQL, st.Args, tt.sql, tt.args)
			}
		})
	}
}

func TestSelectRejectsBadInput(t *testing.T) {
	bad := []struct {
		name string
		w    Where
	}{
		{"unknown operator", Where{Conditions: []Condition{{"a", "regex", 1}}}},
		{"empty column", Where{Conditions: []Condition{{"", OpEqual, 1}}}},
		{"unknown combinator", Where{Combinator: "xor", Conditions: []Condition{{"a", OpEqual, 1}}}},
	}
	for _, tt := range bad {
		if _, err := Postgres.Select(Table{Name: "t"}, nil, tt.w, Sort{}, 0); err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
	if _, err := Postgres.Select(Table{Name: ""}, nil, Where{}, Sort{}, 0); err == nil {
		t.Error("empty table must fail")
	}
}

func TestInjectionStaysInsideQuotesAndParams(t *testing.T) {
	st, err := Postgres.Select(Table{Name: `t"; DROP TABLE t; --`}, []string{`x" FROM y; --`},
		Where{Conditions: []Condition{{"a", OpEqual, "1' OR '1'='1"}}}, Sort{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st.SQL, "1' OR") {
		t.Errorf("value leaked into SQL: %s", st.SQL)
	}
	want := `SELECT "x"" FROM y; --" FROM "t""; DROP TABLE t; --" WHERE "a" = $1`
	if st.SQL != want {
		t.Errorf("got %s\nwant %s", st.SQL, want)
	}
}

func TestInsert(t *testing.T) {
	row := []Pair{{"id", 1.0}, {"meta", map[string]any{"k": "v"}}, {"note", nil}}
	st, err := Postgres.Insert(Table{Schema: "s", Name: "t"}, row)
	if err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "s"."t" ("id", "meta", "note") VALUES ($1, $2, $3) RETURNING *`; st.SQL != want || !st.ReturnsRows {
		t.Errorf("got %q rows=%v", st.SQL, st.ReturnsRows)
	}
	if want := []any{int64(1), `{"k":"v"}`, nil}; !reflect.DeepEqual(st.Args, want) {
		t.Errorf("args %#v", st.Args)
	}
	my, _ := MySQL.Insert(Table{Name: "t"}, row[:1])
	if my.SQL != "INSERT INTO `t` (`id`) VALUES (?)" || my.ReturnsRows {
		t.Errorf("mysql: %q rows=%v", my.SQL, my.ReturnsRows)
	}
	empty, _ := Postgres.Insert(Table{Name: "t"}, nil)
	if empty.SQL != `INSERT INTO "t" DEFAULT VALUES RETURNING *` {
		t.Errorf("empty pg: %q", empty.SQL)
	}
	empty, _ = MySQL.Insert(Table{Name: "t"}, nil)
	if empty.SQL != "INSERT INTO `t` () VALUES ()" {
		t.Errorf("empty mysql: %q", empty.SQL)
	}
}

func TestUpdateParameterOrder(t *testing.T) {
	set := []Pair{{"name", "bob"}, {"age", 3.0}}
	match := []Pair{{"id", 7.0}, {"tenant", nil}}
	st, err := Postgres.Update(Table{Name: "u"}, set, match)
	if err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "u" SET "name" = $1, "age" = $2 WHERE "id" = $3 AND "tenant" IS NULL RETURNING *`; st.SQL != want {
		t.Errorf("got %q", st.SQL)
	}
	if want := []any{"bob", int64(3), int64(7)}; !reflect.DeepEqual(st.Args, want) {
		t.Errorf("args %#v", st.Args)
	}
	my, _ := MySQL.Update(Table{Name: "u"}, set, match[:1])
	if want := "UPDATE `u` SET `name` = ?, `age` = ? WHERE `id` = ?"; my.SQL != want {
		t.Errorf("mysql got %q", my.SQL)
	}
	if _, err := Postgres.Update(Table{Name: "u"}, nil, match); err == nil {
		t.Error("no set columns must fail")
	}
	if _, err := Postgres.Update(Table{Name: "u"}, set, nil); err == nil {
		t.Error("no match columns must fail")
	}
}

func TestUpsert(t *testing.T) {
	row := []Pair{{"id", 1.0}, {"name", "a"}}
	pg, err := Postgres.Upsert(Table{Name: "t"}, row, []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "t" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name" RETURNING *`; pg.SQL != want {
		t.Errorf("pg got %q", pg.SQL)
	}
	my, _ := MySQL.Upsert(Table{Name: "t"}, row, []string{"id"})
	if want := "INSERT INTO `t` (`id`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)"; my.SQL != want {
		t.Errorf("mysql got %q", my.SQL)
	}
	pgOnly, _ := Postgres.Upsert(Table{Name: "t"}, row[:1], []string{"id"})
	if want := `INSERT INTO "t" ("id") VALUES ($1) ON CONFLICT ("id") DO NOTHING RETURNING *`; pgOnly.SQL != want {
		t.Errorf("pg do nothing got %q", pgOnly.SQL)
	}
	myOnly, _ := MySQL.Upsert(Table{Name: "t"}, row[:1], []string{"id"})
	if want := "INSERT INTO `t` (`id`) VALUES (?) ON DUPLICATE KEY UPDATE `id` = `id`"; myOnly.SQL != want {
		t.Errorf("mysql noop got %q", myOnly.SQL)
	}
	if _, err := Postgres.Upsert(Table{Name: "t"}, row, nil); err == nil {
		t.Error("missing match columns must fail")
	}
}

func TestDelete(t *testing.T) {
	st, err := MySQL.Delete(Table{Name: "t"}, Where{Conditions: []Condition{{"id", OpLess, 5.0}}})
	if err != nil {
		t.Fatal(err)
	}
	if st.SQL != "DELETE FROM `t` WHERE `id` < ?" || st.ReturnsRows || !reflect.DeepEqual(st.Args, []any{int64(5)}) {
		t.Errorf("got %#v", st)
	}
	if _, err := Postgres.Delete(Table{Name: "t"}, Where{}); err == nil {
		t.Error("delete without conditions must fail")
	}
}

func TestReturnsRows(t *testing.T) {
	tests := map[string]bool{
		"SELECT 1": true, "  select * from t": true, "-- c\nSELECT 1": true, "/* c */ WITH x AS (SELECT 1) SELECT * FROM x": true,
		"(SELECT 1) UNION (SELECT 2)": true, "SHOW TABLES": true, "insert into t values (1) returning id": true,
		"INSERT INTO t VALUES (1)": false, "update t set a = 1": false, "DELETE FROM t": false, "CREATE TABLE t (a int)": false,
	}
	for q, want := range tests {
		if got := returnsRows(q); got != want {
			t.Errorf("returnsRows(%q) = %v, want %v", q, got, want)
		}
	}
}
