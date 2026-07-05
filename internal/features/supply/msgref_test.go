package supply

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMessageRef(t *testing.T) {
	const guild = "111111111111111111"
	const ch = "222222222222222222"
	const msg = "333333333333333333"

	ok := []string{
		"https://discord.com/channels/" + guild + "/" + ch + "/" + msg,
		"https://discordapp.com/channels/" + guild + "/" + ch + "/" + msg,
		"https://canary.discord.com/channels/" + guild + "/" + ch + "/" + msg,
		"https://ptb.discord.com/channels/" + guild + "/" + ch + "/" + msg,
		"  https://discord.com/channels/" + guild + "/" + ch + "/" + msg + "/  ", // trailing slash + whitespace
	}
	for _, link := range ok {
		ref, valid := parseMessageRef(link, guild)
		assert.Truef(t, valid, "should accept %q", link)
		assert.Equal(t, guild, ref.GuildID)
		assert.Equal(t, ch, ref.ChannelID)
		assert.Equal(t, msg, ref.MessageID)
		// Round-trips back to a canonical link.
		assert.Equal(t, "https://discord.com/channels/"+guild+"/"+ch+"/"+msg, ref.Link())
	}

	bad := []string{
		"",
		"not a link",
		"http://discord.com/channels/" + guild + "/" + ch + "/" + msg,             // http, not https
		"https://discord.com/channels/" + guild + "/" + ch,                        // missing message
		"https://discord.com/channels/" + guild + "/" + ch + "/" + msg + "/extra", // extra segment
		"https://example.com/channels/" + guild + "/" + ch + "/" + msg,            // wrong host
		"https://discord.com/channels/" + guild + "/" + ch + "/abc",               // non-numeric message
	}
	for _, link := range bad {
		_, valid := parseMessageRef(link, guild)
		assert.Falsef(t, valid, "should reject %q", link)
	}

	// A link from a different guild is rejected even if well-formed.
	_, valid := parseMessageRef("https://discord.com/channels/999/"+ch+"/"+msg, guild)
	assert.False(t, valid, "guild mismatch is rejected")
}
