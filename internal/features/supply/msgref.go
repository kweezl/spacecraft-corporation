package supply

import (
	"regexp"
	"strings"
)

// msgLinkRe matches a canonical Discord message link:
//
//	https://discord.com/channels/<guild>/<channel>/<message>
//
// It tolerates the discordapp.com alias, the canary/ptb subdomains, and an
// optional trailing slash. The three captures are snowflake ids (digits only).
var msgLinkRe = regexp.MustCompile(`^https://(?:(?:canary|ptb)\.)?discord(?:app)?\.com/channels/(\d+)/(\d+)/(\d+)/?$`)

// parseMessageRef parses a Discord message link and validates that it points at
// wantGuild (the current request's server snowflake). ok is false for a
// malformed link, a wrong host/format, or a link from another guild. The URL is
// never stored — only the returned identifiers are.
func parseMessageRef(link, wantGuild string) (MessageRef, bool) {
	m := msgLinkRe.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return MessageRef{}, false
	}
	if m[1] != wantGuild {
		return MessageRef{}, false
	}
	return MessageRef{GuildID: m[1], ChannelID: m[2], MessageID: m[3]}, true
}
