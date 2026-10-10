package soap

import (
	"regexp"
	"strconv"
	"strings"
)

// ServerInfo holds metrics parsed from `.server info` output. Every field is
// optional: output shape varies between AzerothCore builds, and the queue line
// is only printed when a queue exists. Absent metrics are simply omitted from
// the embed.
type ServerInfo struct {
	Uptime        string // e.g. "1 Day 2 Hours 15 Minutes"; "" when not reported
	Queue         *int   // nil when the core reports no queue
	PlayersOnline *int   // connected players; nil when not reported
}

// Current AzerothCore master prints (acore_string 11032/11033/11023/13):
//
//	Connected players: {} (peak: {}).
//	Connected players: {} (peak: {}). Queue: {}.
//	Characters in world: {} (peak: {}, lifetime: {}).
//	Server uptime: {}
//
// Older builds use shapes like "Players online: X" / "Player count: X".
var (
	uptimeRe  = regexp.MustCompile(`(?i)server uptime\s*:?\s*(.+)`)
	queueRe   = regexp.MustCompile(`(?i)queue\s*:?\s*(\d+)`)
	playersRe = regexp.MustCompile(`(?i)(?:connected players?|players? online|online players?|player count)\s*:?\s*(\d+)`)
)

// ParseServerInfo extracts the optional metrics from `.server info` output.
func ParseServerInfo(text string) ServerInfo {
	var info ServerInfo
	for _, line := range strings.Split(text, "\n") {
		if m := uptimeRe.FindStringSubmatch(line); m != nil {
			info.Uptime = strings.TrimSpace(m[1])
		}
		if m := queueRe.FindStringSubmatch(line); m != nil {
			if q, err := strconv.Atoi(m[1]); err == nil {
				info.Queue = &q
			}
		}
		if m := playersRe.FindStringSubmatch(line); m != nil {
			if p, err := strconv.Atoi(m[1]); err == nil {
				info.PlayersOnline = &p
			}
		}
	}
	return info
}
