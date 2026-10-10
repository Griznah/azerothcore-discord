package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Griznah/azerothcore-discord/internal/mysql"
	"github.com/Griznah/azerothcore-discord/internal/soap"
	"github.com/bwmarrin/discordgo"
)

func writeTestConfig(t *testing.T, path string, cfg *Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func validTestConfig() *Config {
	return &Config{
		ChannelID:      "123456789",
		UpdateInterval: 30,
		Realms: []Realm{
			{
				Name: "Test Realm",
				SOAP: SOAPConfig{
					Host: "wow.example.com",
					Port: 7878,
					User: "soapuser",
					Pass: "soappass",
				},
				CharactersDSN: "user:pass@tcp(characters.example.com:3306)/acore_characters",
				AuthDSN:       "user:pass@tcp(auth.example.com:3306)/acore_auth",
			},
		},
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeTestConfig(t, path, validTestConfig())

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if len(cfg.Realms) != 1 || cfg.Realms[0].Name != "Test Realm" {
		t.Fatalf("unexpected realms: %+v", cfg.Realms)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("missing config file must not be an error, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeTestConfig(t, path, validTestConfig())

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Blacklist prefixes default to ["RNDBOT"]
	if len(cfg.BlacklistAccountPrefixes) != 1 || cfg.BlacklistAccountPrefixes[0] != "RNDBOT" {
		t.Fatalf("expected default blacklist [RNDBOT], got: %v", cfg.BlacklistAccountPrefixes)
	}

	// Player list cap defaults to 25
	if cfg.Realms[0].PlayerListCap != defaultPlayerListCap {
		t.Fatalf("expected default player_list_cap %d, got: %d", defaultPlayerListCap, cfg.Realms[0].PlayerListCap)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"no realms", func(c *Config) { c.Realms = nil }, "realms cannot be empty"},
		{"empty realm name", func(c *Config) { c.Realms[0].Name = "" }, "empty name"},
		{"empty soap host", func(c *Config) { c.Realms[0].SOAP.Host = "" }, "empty soap.host"},
		{"invalid soap port", func(c *Config) { c.Realms[0].SOAP.Port = 0 }, "invalid soap.port"},
		{"empty soap user", func(c *Config) { c.Realms[0].SOAP.User = "" }, "empty soap.user"},
		{"missing characters dsn", func(c *Config) { c.Realms[0].CharactersDSN = "" }, "empty characters_dsn"},
		{"missing auth dsn", func(c *Config) { c.Realms[0].AuthDSN = "" }, "empty auth_dsn"},
		{"zero update interval", func(c *Config) { c.UpdateInterval = 0 }, "update_interval"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.mutate(cfg)
			err := validateConfigStructSafeRuntime(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
			} else if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestConfigManagerGetConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := validTestConfig()
	writeTestConfig(t, path, cfg)

	cm := NewConfigManager(path, cfg)
	if cm.GetConfig() != cfg {
		t.Fatal("GetConfig must return the stored config")
	}
}

func TestConfigManagerReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := validTestConfig()
	writeTestConfig(t, path, cfg)

	cm := NewConfigManager(path, cfg)

	// No change: reload is a no-op, same config stays
	if err := cm.checkAndReloadIfNeeded(); err != nil {
		t.Fatalf("reload with unchanged file failed: %v", err)
	}
	if cm.GetConfig() != cfg {
		t.Fatal("config must be unchanged without file change")
	}

	// Modify file: new realm list must be picked up
	cfg.Realms = append(cfg.Realms, Realm{
		Name:          "Second Realm",
		SOAP:          SOAPConfig{Host: "wow2.example.com", Port: 7879, User: "u", Pass: "p"},
		CharactersDSN: "user:pass@tcp(characters2.example.com:3306)/acore_characters2",
		AuthDSN:       "user:pass@tcp(auth.example.com:3306)/acore_auth",
	})
	writeTestConfig(t, path, cfg)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := cm.checkAndReloadIfNeeded(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	reloaded := cm.GetConfig()
	if reloaded == cfg {
		t.Fatal("expected a new config object after reload")
	}
	if len(reloaded.Realms) != 2 {
		t.Fatalf("expected 2 realms after reload, got %d", len(reloaded.Realms))
	}
}

func TestConfigManagerReloadKeepsOldConfigOnInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := validTestConfig()
	writeTestConfig(t, path, cfg)

	cm := NewConfigManager(path, cfg)

	// Write an invalid config (empty DSN)
	bad := validTestConfig()
	bad.Realms[0].CharactersDSN = ""
	writeTestConfig(t, path, bad)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := cm.checkAndReloadIfNeeded(); err == nil {
		t.Fatal("expected validation error on reload with invalid config")
	}
	if cm.GetConfig() != cfg {
		t.Fatal("old config must be kept when new config is invalid")
	}
}

func TestResolveChannelID(t *testing.T) {
	t.Setenv("CHANNEL_ID", "env-channel")

	if got := resolveChannelID(validTestConfig()); got != "123456789" {
		t.Fatalf("config channel must win, got: %s", got)
	}

	cfg := validTestConfig()
	cfg.ChannelID = ""
	if got := resolveChannelID(cfg); got != "env-channel" {
		t.Fatalf("env fallback expected, got: %s", got)
	}

	if got := resolveChannelID(nil); got != "env-channel" {
		t.Fatalf("env fallback for nil config expected, got: %s", got)
	}
}

func TestChannelForRealm(t *testing.T) {
	t.Setenv("CHANNEL_ID", "env-channel")

	cfg := validTestConfig() // top-level channel 123456789

	// Per-realm channel wins
	realm := cfg.Realms[0]
	realm.ChannelID = "realm-channel"
	if got := channelForRealm(cfg, realm); got != "realm-channel" {
		t.Fatalf("per-realm channel must win, got: %s", got)
	}

	// Top-level config channel next
	if got := channelForRealm(cfg, cfg.Realms[0]); got != "123456789" {
		t.Fatalf("top-level config channel expected, got: %s", got)
	}

	// Env fallback last
	cfg.ChannelID = ""
	if got := channelForRealm(cfg, cfg.Realms[0]); got != "env-channel" {
		t.Fatalf("env fallback expected, got: %s", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"password=hunter2", "password=[REDACTED]"},
		{"nothing to redact", "nothing to redact"},
	}
	for _, tt := range tests {
		if got := RedactSecrets(tt.in); got != tt.want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func intPtr(v int) *int { return &v }

func onlineStatus(realm Realm, uptime string, queue, connected *int, chars []mysql.Character, total int) realmStatus {
	return realmStatus{
		realm:     realm,
		online:    true,
		info:      soap.ServerInfo{Uptime: uptime, Queue: queue, PlayersOnline: connected},
		realChars: chars,
		total:     total,
	}
}

func TestRealmFieldOffline(t *testing.T) {
	st := realmStatus{realm: Realm{Name: "Dead Realm"}}
	field := realmField(st)
	if field.Name != "🔴 Dead Realm" {
		t.Fatalf("field name = %q", field.Name)
	}
	if field.Value != "Offline" {
		t.Fatalf("offline realm must show Offline, got %q", field.Value)
	}
}

func TestRealmFieldOnline(t *testing.T) {
	realm := Realm{Name: "My Realm", PlayerListCap: 25}
	chars := []mysql.Character{
		{Name: "Alice", Class: 8, Level: 80},
		{Name: "Bob", Class: 5, Level: 42},
	}
	st := onlineStatus(realm, "1 Day 2 Hours", intPtr(7), intPtr(130), chars, 4)

	field := realmField(st)
	if field.Name != "🟢 My Realm" {
		t.Fatalf("field name = %q", field.Name)
	}
	for _, want := range []string{
		"**Uptime:** 1 Day 2 Hours",
		"**Queue:** 7",
		"**Connected:** 130",
		"**Players:** 2 / 4",
		"Alice (80 Mage)",
		"Bob (42 Priest)",
	} {
		if !strings.Contains(field.Value, want) {
			t.Errorf("field value missing %q:\n%s", want, field.Value)
		}
	}
}

func TestRealmFieldOmitsAbsentMetrics(t *testing.T) {
	realm := Realm{Name: "Sparse"}
	st := onlineStatus(realm, "", nil, nil, nil, 0)

	field := realmField(st)
	for _, absent := range []string{"**Uptime:**", "**Queue:**", "**Connected:**"} {
		if strings.Contains(field.Value, absent) {
			t.Errorf("absent metric %q must be omitted:\n%s", absent, field.Value)
		}
	}
	if !strings.Contains(field.Value, "**Players:** 0 / 0") {
		t.Errorf("players count expected:\n%s", field.Value)
	}
}

func TestRealmFieldListUnavailable(t *testing.T) {
	st := onlineStatus(Realm{Name: "Broken"}, "2 Hours", nil, nil, nil, 0)
	st.listErr = fmt.Errorf("db down")

	field := realmField(st)
	if !strings.Contains(field.Value, "**Players:** list unavailable") {
		t.Fatalf("expected list unavailable:\n%s", field.Value)
	}
}

func TestRealmFieldPlayerListCap(t *testing.T) {
	realm := Realm{Name: "Busy", PlayerListCap: 2}
	chars := make([]mysql.Character, 5)
	for i := range chars {
		chars[i] = mysql.Character{Name: fmt.Sprintf("P%d", i), Class: 1, Level: 80}
	}
	st := onlineStatus(realm, "", nil, nil, chars, 5)

	field := realmField(st)
	if !strings.Contains(field.Value, "P0 (80 Warrior)") || !strings.Contains(field.Value, "P1 (80 Warrior)") {
		t.Fatalf("first two players expected:\n%s", field.Value)
	}
	if strings.Contains(field.Value, "P2 (") {
		t.Fatalf("players beyond cap must be cut:\n%s", field.Value)
	}
	if !strings.Contains(field.Value, "+3 more") {
		t.Fatalf("overflow line expected:\n%s", field.Value)
	}
}

func TestBuildChannelEmbeds(t *testing.T) {
	t.Setenv("CHANNEL_ID", "")
	cfg := &Config{
		ChannelID:      "chan-top",
		UpdateInterval: 30,
		Realms: []Realm{
			{Name: "A"},
			{Name: "B", ChannelID: "chan-b"},
			{Name: "C"},
		},
	}
	statuses := []realmStatus{
		{realm: cfg.Realms[0], online: false},
		onlineStatus(cfg.Realms[1], "2 Hours", nil, nil, nil, 0),
		onlineStatus(cfg.Realms[2], "", nil, nil, nil, 0),
	}

	embeds := buildChannelEmbeds(cfg, statuses)
	if len(embeds) != 2 {
		t.Fatalf("expected 2 channel embeds, got %d", len(embeds))
	}

	// Realms A and C share chan-top (one embed, two fields); B has its own
	byChannel := map[string]*discordgo.MessageEmbed{}
	for _, ce := range embeds {
		byChannel[ce.channelID] = ce.embed
	}
	if len(byChannel["chan-top"].Fields) != 2 {
		t.Fatalf("chan-top must have 2 fields, got %d", len(byChannel["chan-top"].Fields))
	}
	if byChannel["chan-top"].Fields[0].Name != "🔴 A" {
		t.Fatalf("first chan-top field must be offline realm A, got %q", byChannel["chan-top"].Fields[0].Name)
	}
	if len(byChannel["chan-b"].Fields) != 1 {
		t.Fatalf("chan-b must have 1 field, got %d", len(byChannel["chan-b"].Fields))
	}
}

func TestBuildChannelEmbedsEnvFallback(t *testing.T) {
	t.Setenv("CHANNEL_ID", "chan-env")
	cfg := &Config{
		UpdateInterval: 30,
		Realms:         []Realm{{Name: "A"}},
	}
	statuses := []realmStatus{{realm: cfg.Realms[0], online: false}}

	embeds := buildChannelEmbeds(cfg, statuses)
	if len(embeds) != 1 || embeds[0].channelID != "chan-env" {
		t.Fatalf("expected env-fallback channel chan-env, got %+v", embeds)
	}
}

func TestBuildChannelEmbedsSkipsUnchanneledRealm(t *testing.T) {
	t.Setenv("CHANNEL_ID", "")
	cfg := &Config{
		UpdateInterval: 30,
		Realms:         []Realm{{Name: "Nowhere"}},
	}
	statuses := []realmStatus{{realm: cfg.Realms[0], online: false}}

	if embeds := buildChannelEmbeds(cfg, statuses); len(embeds) != 0 {
		t.Fatalf("realm without channel must be skipped, got %d embeds", len(embeds))
	}
}
