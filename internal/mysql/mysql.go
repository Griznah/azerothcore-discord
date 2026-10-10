// Package mysql queries one AzerothCore realm's characters database and the
// shared auth database for the Discord status bot.
package mysql

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

// onlineCharactersQuery lists online characters with their owning account id,
// alphabetical. Blacklisting happens in Go (FilterBlacklisted), never in SQL:
// RNDBOT-style deployments have thousands of accounts and a NOT IN (...) clause
// would be unbounded.
const onlineCharactersQuery = "SELECT name, race, class, level, account FROM characters WHERE online = 1 ORDER BY name ASC"

// accountIDsByPrefixQuery finds auth account ids whose username starts with a
// given prefix (MySQL LIKE is case-insensitive under default collation).
const accountIDsByPrefixQuery = "SELECT id FROM account WHERE username LIKE ?"

// Character is one online character.
type Character struct {
	Name    string
	Race    uint8
	Class   uint8
	Level   uint8
	Account uint32
}

// rows abstracts *sql.Rows so queries can be tested with fake data.
type rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// querier abstracts *sql.DB so queries can be tested with fake data.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (rows, error)
}

// sqlQuerier adapts *sql.DB to querier.
type sqlQuerier struct{ db *sql.DB }

func (s sqlQuerier) QueryContext(ctx context.Context, query string, args ...any) (rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

// Client queries one realm's characters database and the auth database.
type Client struct {
	characters querier
	auth       querier
	closeFns   []func() error
}

// New opens both databases. Connections are lazy; per-query failures surface
// on the refresh that triggers them.
func New(charactersDSN, authDSN string) (*Client, error) {
	charactersDB, err := sql.Open("mysql", charactersDSN)
	if err != nil {
		return nil, fmt.Errorf("open characters db: %w", err)
	}
	authDB, err := sql.Open("mysql", authDSN)
	if err != nil {
		charactersDB.Close()
		return nil, fmt.Errorf("open auth db: %w", err)
	}
	return &Client{
		characters: sqlQuerier{charactersDB},
		auth:       sqlQuerier{authDB},
		closeFns:   []func() error{charactersDB.Close, authDB.Close},
	}, nil
}

// Close closes both database handles.
func (c *Client) Close() error {
	var firstErr error
	for _, fn := range c.closeFns {
		if err := fn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// OnlineCharacters returns all online characters of the realm, name ASC.
func (c *Client) OnlineCharacters(ctx context.Context) ([]Character, error) {
	rs, err := c.characters.QueryContext(ctx, onlineCharactersQuery)
	if err != nil {
		return nil, fmt.Errorf("query online characters: %w", err)
	}
	defer rs.Close()

	var chars []Character
	for rs.Next() {
		var name string
		var race, class, level, account int64
		if err := rs.Scan(&name, &race, &class, &level, &account); err != nil {
			return nil, fmt.Errorf("scan character row: %w", err)
		}
		chars = append(chars, Character{
			Name:    name,
			Race:    uint8(race),
			Class:   uint8(class),
			Level:   uint8(level),
			Account: uint32(account),
		})
	}
	return chars, rs.Err()
}

// BlacklistedAccountIDs returns auth account ids whose username starts with
// any of the given prefixes. Fetch once per refresh and reuse for all realms.
func (c *Client) BlacklistedAccountIDs(ctx context.Context, prefixes []string) (map[uint32]bool, error) {
	blacklist := make(map[uint32]bool)
	for _, prefix := range prefixes {
		rs, err := c.auth.QueryContext(ctx, accountIDsByPrefixQuery, prefix+"%")
		if err != nil {
			return nil, fmt.Errorf("query accounts with prefix %q: %w", prefix, err)
		}
		for rs.Next() {
			var id int64
			if err := rs.Scan(&id); err != nil {
				rs.Close()
				return nil, fmt.Errorf("scan account id row: %w", err)
			}
			blacklist[uint32(id)] = true
		}
		if err := rs.Err(); err != nil {
			rs.Close()
			return nil, fmt.Errorf("iterate accounts with prefix %q: %w", prefix, err)
		}
		rs.Close()
	}
	return blacklist, nil
}

// FilterBlacklisted splits chars into real players and blacklisted (e.g.
// RNDBOT-owned) ones. The blacklist affects the player list only; total counts
// still include the hidden characters.
func FilterBlacklisted(chars []Character, blacklist map[uint32]bool) (real, hidden []Character) {
	for _, ch := range chars {
		if blacklist[ch.Account] {
			hidden = append(hidden, ch)
		} else {
			real = append(real, ch)
		}
	}
	return real, hidden
}

// WotLK race and class ids → display names.
var (
	raceNames = map[uint8]string{
		1: "Human", 2: "Orc", 3: "Dwarf", 4: "Night Elf", 5: "Undead",
		6: "Tauren", 7: "Gnome", 8: "Troll", 10: "Blood Elf", 11: "Draenei",
	}
	classNames = map[uint8]string{
		1: "Warrior", 2: "Paladin", 3: "Hunter", 4: "Rogue", 5: "Priest",
		6: "Death Knight", 7: "Shaman", 8: "Mage", 9: "Warlock", 11: "Druid",
	}
)

// RaceName returns the display name for a race id ("Unknown" if unmapped).
func RaceName(id uint8) string {
	if name, ok := raceNames[id]; ok {
		return name
	}
	return "Unknown"
}

// ClassName returns the display name for a class id ("Unknown" if unmapped).
func ClassName(id uint8) string {
	if name, ok := classNames[id]; ok {
		return name
	}
	return "Unknown"
}
