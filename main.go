package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Griznah/azerothcore-discord/internal/mysql"
	"github.com/Griznah/azerothcore-discord/internal/soap"
	"github.com/bwmarrin/discordgo"
)

// ================= SECRET REDACTION =================
// RedactSecrets replaces secrets/patterns in logs with [REDACTED]
func RedactSecrets(s string) string {
	patterns := []string{
		`(?i)(api[_-]?key|token|secret|bearer)["'=: ]+([a-zA-Z0-9\-_.:]+)`, // API_KEY=xxx, Bearer ...
		`(?i)(password)["'=: ]+([a-zA-Z0-9\-_.:]+)`,                        // password fields
	}
	for _, pat := range patterns {
		re := regexp.MustCompile(pat)
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			// Only redact value part, not the key
			parts := strings.SplitN(m, "=", 2)
			if len(parts) == 2 {
				return parts[0] + "=[REDACTED]"
			}
			colon := strings.SplitN(m, ":", 2)
			if len(colon) == 2 {
				return colon[0] + ": [REDACTED]"
			}
			space := strings.SplitN(m, " ", 2)
			if len(space) == 2 {
				return space[0] + " [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return s
}

type redactingWriter struct{ underlying io.Writer }

func (rw *redactingWriter) Write(p []byte) (int, error) {
	redacted := RedactSecrets(string(p))
	_, err := rw.underlying.Write([]byte(redacted))
	return len(p), err
}

// Call this at program start: makes all log.Print log.Printf secrets-safe
func InstallRedactingLogger() {
	log.SetOutput(&redactingWriter{underlying: os.Stderr})
}

// ================= ENV LOADING =================

// loadEnv reads a .env file and sets environment variables
// Only sets variables that aren't already set in the environment
func loadEnv() error {
	envPath := ".env"

	file, err := os.Open(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			// .env file is optional, not an error
			return nil
		}
		return fmt.Errorf("failed to open .env file: %w", err)
	}
	defer file.Close()

	log.Printf("Loading environment variables from: %s", envPath)

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			log.Printf("Warning: invalid line %d in .env, skipping: %s", lineNum, line)
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = strings.Trim(value, "\"")
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			value = strings.Trim(value, "'")
		}

		// Only set if not already in environment
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				log.Printf("Warning: failed to set %s: %v", key, err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading .env file: %w", err)
	}

	return nil
}

// ================= CONFIG =================

const defaultPlayerListCap = 25

// SOAPConfig points at a worldserver SOAP (remote access) endpoint.
type SOAPConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

// Realm is one AzerothCore realm monitored by the bot.
type Realm struct {
	Name          string     `json:"name"`
	SOAP          SOAPConfig `json:"soap"`
	CharactersDSN string     `json:"characters_dsn"`
	AuthDSN       string     `json:"auth_dsn"`
	ChannelID     string     `json:"channel_id,omitempty"`      // optional: overrides top-level channel
	PlayerListCap int        `json:"player_list_cap,omitempty"` // optional: overrides default 25
}

// Config holds application configuration loaded from config.json
type Config struct {
	ChannelID                string   `json:"channel_id"`
	UpdateInterval           int      `json:"update_interval"`
	BlacklistAccountPrefixes []string `json:"blacklist_account_prefixes"`
	Realms                   []Realm  `json:"realms"`
}

// applyDefaults fills in unset optional values with their defaults.
func applyDefaults(cfg *Config) {
	if len(cfg.BlacklistAccountPrefixes) == 0 {
		cfg.BlacklistAccountPrefixes = []string{"RNDBOT"}
	}
	for i := range cfg.Realms {
		if cfg.Realms[i].PlayerListCap == 0 {
			cfg.Realms[i].PlayerListCap = defaultPlayerListCap
		}
	}
}

// ConfigManager provides thread-safe access to configuration with dynamic reload
// Uses atomic.Value for lock-free reads (critical for performance during polling)
// Uses sync.RWMutex to serialize reload operations (rare writes vs frequent reads)
type ConfigManager struct {
	config      atomic.Value // stores *Config
	configPath  string
	lastModTime time.Time
	mu          sync.RWMutex
}

// NewConfigManager creates a new ConfigManager with an initial configuration
// Stores initial config in atomic.Value for lock-free access
// Records initial file modification time to detect future changes
func NewConfigManager(configPath string, initial *Config) *ConfigManager {
	cm := &ConfigManager{
		configPath: configPath,
	}
	cm.config.Store(initial)

	// Get initial file modification time (only if config exists)
	if initial != nil {
		if modTime, err := cm.getLastModTime(); err == nil {
			cm.lastModTime = modTime
		} else {
			log.Printf("Warning: failed to get initial config mod time: %v", err)
		}
	}

	return cm
}

// GetConfig returns the current configuration (thread-safe, lock-free read)
func (cm *ConfigManager) GetConfig() *Config {
	val := cm.config.Load()
	if val == nil {
		return nil
	}
	return val.(*Config)
}

// getLastModTime retrieves the modification time of the config file (changes indicate config modifications requiring reload)
// Returns raw os.Stat error for caller to handle (file not found, permission denied, etc.)
func (cm *ConfigManager) getLastModTime() (time.Time, error) {
	info, err := os.Stat(cm.configPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// checkAndReloadIfNeeded checks if the config file has changed and reloads synchronously.
// Uses a short 5ms debounce to batch rapid file writes (e.g., editor saves).
// Returns after reload completes (or immediately if no change detected).
// Holds the lock during the entire operation to prevent race conditions.
func (cm *ConfigManager) checkAndReloadIfNeeded() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check current file modification time
	currentModTime, err := cm.getLastModTime()
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Config file not found, skipping reload")
			return nil
		}
		return fmt.Errorf("failed to stat config file: %w", err)
	}

	// No change detected
	if currentModTime.Equal(cm.lastModTime) || currentModTime.Before(cm.lastModTime) {
		return nil
	}

	// File has changed, wait briefly to batch rapid writes
	time.Sleep(5 * time.Millisecond)

	// Re-check file modification time after debounce
	currentModTime, err = cm.getLastModTime()
	if err != nil {
		return fmt.Errorf("failed to stat config file: %w", err)
	}

	if currentModTime.Equal(cm.lastModTime) || currentModTime.Before(cm.lastModTime) {
		return nil
	}

	log.Printf("Config file modified, attempting reload from: %s", cm.configPath)

	// Load new config
	newCfg, err := loadConfig(cm.configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	// If loadConfig returned nil (file not found), skip reload
	if newCfg == nil {
		log.Printf("Config file not found during reload attempt")
		return nil
	}

	// Validate new config; on failure keep serving the old config
	if err := validateConfigStructSafeRuntime(newCfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// Success: atomically swap config and update mod time
	cm.config.Store(newCfg)
	cm.lastModTime = currentModTime
	log.Println("Config reloaded successfully")

	return nil
}

// Cleanup releases resources
// Safe to call multiple times (idempotent)
func (cm *ConfigManager) Cleanup() {
	// No-op: no resources to clean up
}

// loadConfig reads and parses config.json
func loadConfig(providedPath string) (*Config, error) {
	// Determine the config path to use
	configPath := providedPath
	if configPath == "" {
		configPath = "/data/config.json"
	}

	log.Printf("Loading config from: %s", configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Config file not found at %s, starting without config", configPath)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read config from %s: %w", configPath, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config from %s: %w", configPath, err)
	}

	applyDefaults(&cfg)

	log.Printf("Successfully loaded config from: %s", configPath)
	return &cfg, nil
}

// getConfigPath determines the config file path that loadConfig uses
func getConfigPath(providedPath string) string {
	if providedPath != "" {
		return providedPath
	}
	return "/data/config.json"
}

// validateConfigStruct performs fail-fast validation on loaded config
func validateConfigStruct(cfg *Config) {
	if err := validateConfigStructSafeRuntime(cfg); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	log.Printf("Configuration validated: %d realms", len(cfg.Realms))
}

// validateConfigStructSafeRuntime validates a config for runtime reload
// Returns error instead of calling log.Fatalf, allowing bot to continue with old config on validation failure
func validateConfigStructSafeRuntime(cfg *Config) error {
	if cfg.UpdateInterval < 1 {
		return fmt.Errorf("update_interval must be at least 1 second (got: %d)", cfg.UpdateInterval)
	}

	if len(cfg.Realms) == 0 {
		return fmt.Errorf("realms cannot be empty")
	}

	for i, realm := range cfg.Realms {
		if realm.Name == "" {
			return fmt.Errorf("realm at index %d has empty name", i)
		}
		if realm.SOAP.Host == "" {
			return fmt.Errorf("realm '%s' has empty soap.host", realm.Name)
		}
		if realm.SOAP.Port < 1 || realm.SOAP.Port > 65535 {
			return fmt.Errorf("realm '%s' has invalid soap.port: %d (valid range: 1-65535)", realm.Name, realm.SOAP.Port)
		}
		if realm.SOAP.User == "" {
			return fmt.Errorf("realm '%s' has empty soap.user", realm.Name)
		}
		if realm.CharactersDSN == "" {
			return fmt.Errorf("realm '%s' has empty characters_dsn", realm.Name)
		}
		if realm.AuthDSN == "" {
			return fmt.Errorf("realm '%s' has empty auth_dsn", realm.Name)
		}
	}

	return nil
}

// ================= REALM AGGREGATION =================

const (
	soapTimeout    = 10 * time.Second
	refreshTimeout = 20 * time.Second
)

// realmStatus is one realm's aggregated state for a refresh.
type realmStatus struct {
	realm     Realm
	online    bool              // SOAP probe succeeded
	info      soap.ServerInfo   // optional metrics from .server info
	realChars []mysql.Character // online characters, blacklist filtered out
	total     int               // unfiltered online character count
	listErr   error             // characters DB failure, if any
}

// fetchRealmStatuses probes every realm of the config concurrently.
// The account blacklist is fetched once per refresh from the first realm's
// auth DB and shared by all realms.
func fetchRealmStatuses(ctx context.Context, cfg *Config) []realmStatus {
	statuses := make([]realmStatus, len(cfg.Realms))

	var blacklist map[uint32]bool
	if len(cfg.BlacklistAccountPrefixes) > 0 && len(cfg.Realms) > 0 {
		if c, err := mysql.New(cfg.Realms[0].CharactersDSN, cfg.Realms[0].AuthDSN); err == nil {
			blacklist, err = c.BlacklistedAccountIDs(ctx, cfg.BlacklistAccountPrefixes)
			if err != nil {
				log.Printf("Warning: account blacklist fetch failed: %v", err)
			}
			c.Close()
		} else {
			log.Printf("Warning: account blacklist fetch failed: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i, realm := range cfg.Realms {
		wg.Add(1)
		go func(idx int, r Realm) {
			defer wg.Done()
			statuses[idx] = probeRealm(ctx, r, blacklist)
		}(i, realm)
	}
	wg.Wait()
	return statuses
}

func probeRealm(ctx context.Context, r Realm, blacklist map[uint32]bool) realmStatus {
	st := realmStatus{realm: r}

	soapClient := soap.New(r.SOAP.Host, r.SOAP.Port, r.SOAP.User, r.SOAP.Pass, soapTimeout)
	out, err := soapClient.Execute(ctx, "server info")
	if err != nil {
		log.Printf("Realm '%s' offline: %v", r.Name, err)
		return st
	}
	st.online = true
	st.info = soap.ParseServerInfo(out)

	// A down worldserver leaves stale online flags in the characters DB —
	// only query the player list for realms that answered the probe.
	mc, err := mysql.New(r.CharactersDSN, r.AuthDSN)
	if err != nil {
		st.listErr = err
		log.Printf("Realm '%s' characters DB unavailable: %v", r.Name, err)
		return st
	}
	defer mc.Close()
	chars, err := mc.OnlineCharacters(ctx)
	if err != nil {
		st.listErr = err
		log.Printf("Realm '%s' player list query failed: %v", r.Name, err)
		return st
	}
	st.realChars, _ = mysql.FilterBlacklisted(chars, blacklist)
	st.total = len(chars)
	return st
}

// realmField renders one realm as an embed field. Metrics the core did not
// report are omitted; a down realm shows Offline.
func realmField(st realmStatus) *discordgo.MessageEmbedField {
	if !st.online {
		return &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("🔴 %s", st.realm.Name),
			Value:  "Offline",
			Inline: false,
		}
	}

	var b strings.Builder
	if st.info.Uptime != "" {
		fmt.Fprintf(&b, "**Uptime:** %s\n", st.info.Uptime)
	}
	if st.info.PlayersOnline != nil {
		fmt.Fprintf(&b, "**Connected:** %d\n", *st.info.PlayersOnline)
	}
	if st.info.Queue != nil {
		fmt.Fprintf(&b, "**Queue:** %d\n", *st.info.Queue)
	}
	if st.listErr != nil {
		b.WriteString("**Players:** list unavailable\n")
	} else {
		fmt.Fprintf(&b, "**Players:** %d / %d\n", len(st.realChars), st.total)
		for i, ch := range st.realChars {
			if i == st.realm.PlayerListCap {
				fmt.Fprintf(&b, "+%d more\n", len(st.realChars)-st.realm.PlayerListCap)
				break
			}
			fmt.Fprintf(&b, "%s (%d %s)\n", ch.Name, ch.Level, mysql.ClassName(ch.Class))
		}
	}
	return &discordgo.MessageEmbedField{
		Name:   fmt.Sprintf("🟢 %s", st.realm.Name),
		Value:  b.String(),
		Inline: false,
	}
}

// channelEmbed is one embed destined for one Discord channel.
type channelEmbed struct {
	channelID string
	embed     *discordgo.MessageEmbed
}

// buildChannelEmbeds groups realms by resolved channel (per-realm → top-level
// → env) and builds one embed per channel with one field per realm.
func buildChannelEmbeds(cfg *Config, statuses []realmStatus) []channelEmbed {
	byChannel := make(map[string]*discordgo.MessageEmbed)
	var order []string
	for i, st := range statuses {
		channelID := channelForRealm(cfg, cfg.Realms[i])
		if channelID == "" {
			log.Printf("Warning: realm '%s' has no channel configured, skipping", st.realm.Name)
			continue
		}
		embed, ok := byChannel[channelID]
		if !ok {
			embed = &discordgo.MessageEmbed{
				Title: "AzerothCore Realms",
				Color: 0x00FF00, // Green
				Footer: &discordgo.MessageEmbedFooter{
					Text: fmt.Sprintf("Updates every %d seconds", cfg.UpdateInterval),
				},
			}
			byChannel[channelID] = embed
			order = append(order, channelID)
		}
		embed.Fields = append(embed.Fields, realmField(st))
	}

	out := make([]channelEmbed, 0, len(order))
	for _, channelID := range order {
		out = append(out, channelEmbed{channelID: channelID, embed: byChannel[channelID]})
	}
	return out
}

// ================= DISCORD INTEGRATION =================

type Bot struct {
	session        *discordgo.Session
	configManager  *ConfigManager
	statusMessages map[string]*discordgo.Message // one status message per channel
	messageMutex   sync.RWMutex
	readyOnce      sync.Once
}

func (b *Bot) getStatusMessage(channelID string) *discordgo.Message {
	b.messageMutex.RLock()
	defer b.messageMutex.RUnlock()
	return b.statusMessages[channelID]
}

func (b *Bot) setStatusMessage(channelID string, msg *discordgo.Message) {
	b.messageMutex.Lock()
	defer b.messageMutex.Unlock()
	if b.statusMessages == nil {
		b.statusMessages = make(map[string]*discordgo.Message)
	}
	b.statusMessages[channelID] = msg
}

func (b *Bot) updateStatusMessage(channelID string, embed *discordgo.MessageEmbed) error {
	existing := b.getStatusMessage(channelID)

	var msg *discordgo.Message
	var err error

	if existing == nil {
		// Create new message
		msg, err = b.session.ChannelMessageSendEmbed(channelID, embed)
		if err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
		b.setStatusMessage(channelID, msg)
		log.Printf("Initial status message posted in channel %s", channelID)
	} else {
		// Edit existing message
		msg, err = b.session.ChannelMessageEditComplex(
			&discordgo.MessageEdit{
				ID:      existing.ID,
				Channel: channelID,
				Embed:   embed,
			},
		)
		if err != nil {
			// Message might have been deleted - recreate
			if restError, ok := err.(*discordgo.RESTError); ok && restError.Response != nil && restError.Response.StatusCode == 404 {
				msg, err = b.session.ChannelMessageSendEmbed(channelID, embed)
				if err != nil {
					return fmt.Errorf("failed to recreate message: %w", err)
				}
				b.setStatusMessage(channelID, msg)
				log.Printf("Status message recreated in channel %s (previous was deleted)", channelID)
				return nil
			}
			return fmt.Errorf("failed to edit message: %w", err)
		}
		b.setStatusMessage(channelID, msg)
	}

	return nil
}

// ================= EVENT HANDLERS =================

func (b *Bot) onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("✅ Logged in as %s", s.State.User.Username)

	// Discord re-delivers READY on gateway reconnects; run cleanup and loop
	// spawn exactly once per process (issue #39).
	b.readyOnce.Do(func() {
		// Clean up old messages
		if err := b.cleanupOldMessages(); err != nil {
			log.Printf("Warning: cleanup failed: %v", err)
		}

		// Start update loop in background goroutine
		go b.startUpdateLoop()
	})
}

// channelsInUse returns the distinct channels the current config posts to.
func (b *Bot) channelsInUse() []string {
	cfg := b.configManager.GetConfig()
	if cfg == nil {
		return nil
	}
	seen := make(map[string]bool)
	var channels []string
	for _, realm := range cfg.Realms {
		channelID := channelForRealm(cfg, realm)
		if channelID != "" && !seen[channelID] {
			seen[channelID] = true
			channels = append(channels, channelID)
		}
	}
	return channels
}

func (b *Bot) cleanupOldMessages() error {
	for _, channelID := range b.channelsInUse() {
		// Fetch messages (Discord API returns max 100 per request)
		messages, err := b.session.ChannelMessages(channelID, 100, "", "", "")
		if err != nil {
			return fmt.Errorf("failed to fetch messages in %s: %w", channelID, err)
		}

		botUserID := b.session.State.User.ID
		deletedCount := 0

		for _, msg := range messages {
			if msg.Author.ID == botUserID {
				if err := b.session.ChannelMessageDelete(channelID, msg.ID); err != nil {
					log.Printf("Failed to delete message %s: %v", msg.ID, err)
				} else {
					deletedCount++
				}
			}
		}

		log.Printf("Cleaned up %d old bot messages in channel %s", deletedCount, channelID)
	}
	return nil
}

func (b *Bot) registerHandlers() {
	b.session.AddHandler(b.onReady)
}

// ================= UPDATE LOOP =================

func (b *Bot) startUpdateLoop() {
	// Use default interval if no config loaded
	defaultInterval := 30 * time.Second
	cfg := b.configManager.GetConfig()
	interval := defaultInterval
	if cfg != nil {
		interval = time.Duration(cfg.UpdateInterval) * time.Second
	} else {
		log.Printf("No config loaded, using default update interval: %v", defaultInterval)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Track current interval to detect changes
	currentInterval := interval

	// Immediate first update
	b.performUpdate()

	for range ticker.C {
		// Check for config updates before each update
		if err := b.checkForConfigUpdates(); err != nil {
			log.Printf("Config reload check failed: %v", err)
		}

		// Check if interval changed and update ticker
		cfg := b.configManager.GetConfig()
		newInterval := defaultInterval
		if cfg != nil {
			newInterval = time.Duration(cfg.UpdateInterval) * time.Second
		}
		if newInterval != currentInterval {
			ticker.Reset(newInterval)
			currentInterval = newInterval
			log.Printf("Update interval changed to %v", newInterval)
		}

		b.performUpdate()
	}
}

func (b *Bot) performUpdate() {
	cfg := b.configManager.GetConfig()
	if cfg == nil {
		log.Printf("Skipping update: no valid config loaded. Waiting for config...")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	// Fetch all realm info concurrently
	statuses := fetchRealmStatuses(ctx, cfg)

	// Post one embed per channel
	for _, ce := range buildChannelEmbeds(cfg, statuses) {
		if err := b.updateStatusMessage(ce.channelID, ce.embed); err != nil {
			log.Printf("Error updating status in channel %s: %v", ce.channelID, err)
		}
	}
}

// ================= BOT CONSTRUCTION =================

func createDiscordSession(token string) (*discordgo.Session, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentGuildMessages

	return session, nil
}

// NewBot creates a new Bot instance with a Discord session
func NewBot(cfgManager *ConfigManager, token string) (*Bot, error) {
	if token == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN environment variable not set")
	}

	session, err := createDiscordSession(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		session:        session,
		configManager:  cfgManager,
		statusMessages: make(map[string]*discordgo.Message),
	}, nil
}

// Start opens the Discord connection
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	return nil
}

func (b *Bot) WaitForShutdown() {
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)

	<-sigchan
	log.Println("Shutting down...")

	// Cleanup config manager
	if b.configManager != nil {
		b.configManager.Cleanup()
	}

	if err := b.session.Close(); err != nil {
		log.Printf("Error closing Discord session: %v", err)
	}

	log.Println("Shutdown complete")
}

// checkForConfigUpdates wraps checkAndReloadIfNeeded for use in update loop
func (b *Bot) checkForConfigUpdates() error {
	if b.configManager == nil {
		return nil
	}
	return b.configManager.checkAndReloadIfNeeded()
}

// ================= MAIN =================

func validateConfig() (token string, err error) {
	token = os.Getenv("DISCORD_TOKEN")
	if token == "" {
		return "", fmt.Errorf("DISCORD_TOKEN environment variable not set")
	}

	return token, nil
}

// resolveChannelID picks the fallback Discord channel: config channel first, CHANNEL_ID env fallback.
func resolveChannelID(cfg *Config) string {
	if cfg != nil && cfg.ChannelID != "" {
		return cfg.ChannelID
	}
	return os.Getenv("CHANNEL_ID")
}

// channelForRealm resolves a realm's channel: per-realm override → top-level
// config channel → CHANNEL_ID env fallback.
func channelForRealm(cfg *Config, realm Realm) string {
	if realm.ChannelID != "" {
		return realm.ChannelID
	}
	return resolveChannelID(cfg)
}

func checkNotRootUser() {
	if os.Geteuid() == 0 {
		log.Fatalf("SECURITY: Container must not run as root! UID 0 detected. Please rebuild or run with --user/-u flag (see README). Refusing to start.")
	}
}

func main() {
	InstallRedactingLogger()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	checkNotRootUser()

	// Parse command-line flags for config path
	configPath := flag.String("c", "", "Path to config.json file")
	flag.StringVar(configPath, "config", "", "Path to config.json file")
	flag.Parse()

	// Load environment variables from .env file (optional)
	if err := loadEnv(); err != nil {
		log.Printf("Warning: %v", err)
	}

	token, err := validateConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Load and validate config.json
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if cfg == nil {
		log.Printf("Config file not found, starting without config. Waiting for config...")
	} else {
		validateConfigStruct(cfg)
	}

	// Create config manager with initial config (may be nil)
	configManager := NewConfigManager(getConfigPath(*configPath), cfg)
	bot, err := NewBot(configManager, token)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	bot.registerHandlers()

	if err := bot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	// Wait for shutdown signal
	bot.WaitForShutdown()
}
