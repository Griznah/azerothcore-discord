package mysql

import (
	"context"
	"strings"
	"testing"
)

// fakeRows replays canned rows without a database.
type fakeRows struct {
	data [][]any
	i    int
}

func (f *fakeRows) Next() bool {
	f.i++
	return f.i <= len(f.data)
}

func (f *fakeRows) Scan(dest ...any) error {
	row := f.data[f.i-1]
	for i, d := range dest {
		switch ptr := d.(type) {
		case *string:
			*ptr = row[i].(string)
		case *int64:
			*ptr = int64(row[i].(int))
		default:
			panic("unsupported scan dest")
		}
	}
	return nil
}

func (f *fakeRows) Err() error   { return nil }
func (f *fakeRows) Close() error { return nil }

// fakeQuerier records the last query and args, returns canned rows.
type fakeQuerier struct {
	query string
	args  []any
	rows  rows
}

func (f *fakeQuerier) QueryContext(ctx context.Context, query string, args ...any) (rows, error) {
	f.query = query
	f.args = args
	return f.rows, nil
}

func TestOnlineCharacters(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{data: [][]any{
		{"Alice", 1, 8, 80, 5},   // human mage, real account
		{"Bob", 2, 1, 42, 6},     // orc warrior, real account
		{"Zbot", 10, 3, 80, 900}, // RNDBOT-owned account 900
	}}}
	c := &Client{characters: q, auth: &fakeQuerier{}}

	chars, err := c.OnlineCharacters(context.Background())
	if err != nil {
		t.Fatalf("OnlineCharacters failed: %v", err)
	}

	// Exact SQL contract: online-only, account column present, name ASC, no NOT IN
	if q.query != onlineCharactersQuery {
		t.Fatalf("unexpected query: %s", q.query)
	}
	if !strings.Contains(q.query, "WHERE online = 1") {
		t.Fatal("query must select online characters only")
	}
	if !strings.Contains(q.query, "ORDER BY name ASC") {
		t.Fatal("query must order by name ASC")
	}
	if !strings.Contains(q.query, "account") {
		t.Fatal("query must select the account column")
	}
	if strings.Contains(strings.ToUpper(q.query), "NOT IN") {
		t.Fatal("blacklist must never be a NOT IN SQL clause")
	}

	if len(chars) != 3 {
		t.Fatalf("expected 3 characters, got %d", len(chars))
	}
	if chars[0].Name != "Alice" || chars[0].Race != 1 || chars[0].Class != 8 ||
		chars[0].Level != 80 || chars[0].Account != 5 {
		t.Fatalf("unexpected first character: %+v", chars[0])
	}
}

func TestBlacklistedAccountIDs(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{data: [][]any{
		{900}, {901},
	}}}
	c := &Client{characters: &fakeQuerier{}, auth: q}

	blacklist, err := c.BlacklistedAccountIDs(context.Background(), []string{"RNDBOT"})
	if err != nil {
		t.Fatalf("BlacklistedAccountIDs failed: %v", err)
	}

	if q.query != accountIDsByPrefixQuery {
		t.Fatalf("unexpected query: %s", q.query)
	}
	if len(q.args) != 1 || q.args[0] != "RNDBOT%" {
		t.Fatalf("expected LIKE arg RNDBOT%%, got: %v", q.args)
	}
	if !blacklist[900] || !blacklist[901] || len(blacklist) != 2 {
		t.Fatalf("unexpected blacklist: %v", blacklist)
	}
}

func TestBlacklistedAccountIDsMultiplePrefixes(t *testing.T) {
	auth := &prefixQuerier{queries: map[string][][]any{
		"RNDBOT%": {{900}},
		"TEST%":   {{800}},
	}}
	c := &Client{characters: &fakeQuerier{}, auth: auth}

	blacklist, err := c.BlacklistedAccountIDs(context.Background(), []string{"RNDBOT", "TEST"})
	if err != nil {
		t.Fatalf("BlacklistedAccountIDs failed: %v", err)
	}
	if !blacklist[900] || !blacklist[800] || len(blacklist) != 2 {
		t.Fatalf("unexpected blacklist: %v", blacklist)
	}
	if len(auth.got) != 2 || auth.got[0] != "RNDBOT%" || auth.got[1] != "TEST%" {
		t.Fatalf("expected one LIKE query per prefix, got: %v", auth.got)
	}
}

// prefixQuerier answers canned rows per LIKE prefix.
type prefixQuerier struct {
	queries map[string][][]any
	got     []string
}

func (p *prefixQuerier) QueryContext(ctx context.Context, query string, args ...any) (rows, error) {
	prefix, _ := args[0].(string)
	p.got = append(p.got, prefix)
	return &fakeRows{data: p.queries[prefix]}, nil
}

func TestFilterBlacklisted(t *testing.T) {
	chars := []Character{
		{Name: "Alice", Account: 5},
		{Name: "Zbot", Account: 900},
		{Name: "Bob", Account: 6},
		{Name: "Ybot", Account: 901},
	}
	blacklist := map[uint32]bool{900: true, 901: true}

	real, hidden := FilterBlacklisted(chars, blacklist)

	if len(real) != 2 || real[0].Name != "Alice" || real[1].Name != "Bob" {
		t.Fatalf("unexpected real players: %+v", real)
	}
	if len(hidden) != 2 || hidden[0].Name != "Zbot" || hidden[1].Name != "Ybot" {
		t.Fatalf("unexpected hidden players: %+v", hidden)
	}

	// Counts: real / total
	if len(real) != 2 || len(chars) != 4 {
		t.Fatalf("expected counts 2/4, got %d/%d", len(real), len(chars))
	}
}

func TestFilterBlacklistedEmpty(t *testing.T) {
	real, hidden := FilterBlacklisted(nil, map[uint32]bool{1: true})
	if len(real) != 0 || len(hidden) != 0 {
		t.Fatalf("expected empty split, got %d/%d", len(real), len(hidden))
	}
}

func TestRaceAndClassNames(t *testing.T) {
	if RaceName(1) != "Human" || RaceName(10) != "Blood Elf" {
		t.Fatalf("unexpected race names: %q %q", RaceName(1), RaceName(10))
	}
	if RaceName(99) != "Unknown" {
		t.Fatalf("unmapped race must be Unknown, got %q", RaceName(99))
	}
	if ClassName(6) != "Death Knight" || ClassName(11) != "Druid" {
		t.Fatalf("unexpected class names: %q %q", ClassName(6), ClassName(11))
	}
	if ClassName(99) != "Unknown" {
		t.Fatalf("unmapped class must be Unknown, got %q", ClassName(99))
	}
}
