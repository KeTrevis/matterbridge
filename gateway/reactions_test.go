package gateway

import (
	"path/filepath"
	"testing"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionsResolveBothCopiesAfterCacheEviction(t *testing.T) {
	for _, origin := range []string{"slack", "discord"} {
		t.Run(origin, func(t *testing.T) {
			r := persistentTestRouter(t, filepath.Join(t.TempDir(), "links.sqlite3"))
			gw := r.Gateways["bridge1"]
			slack, discord := gw.Bridges["slack.test"], gw.Bridges["discord.test"]
			if origin == "slack" {
				require.NoError(t, gw.rememberMessage("slack 1.0", []*BrMsgID{{discord, "discord root;part2", "generaldiscord.test"}}))
			} else {
				require.NoError(t, gw.rememberMessage("discord root", []*BrMsgID{{slack, "slack 1.0", "testingslack.test"}}))
			}
			gw.Messages.Purge()
			for _, direction := range []struct{ from, id, channel, to, want string }{
				{"slack", "1.0", "testing", "discord", "root"},
				{"discord", "root", "general", "slack", "1.0"},
			} {
				dest := gw.Bridges[direction.to+".test"]
				capture := &threadDestination{}
				dest.Bridger = capture
				msg := &config.Message{ID: direction.id, Channel: direction.channel, Account: direction.from + ".test", Protocol: direction.from, Gateway: "bridge1", Event: config.EventReaction, Reaction: &config.Reaction{Emoji: "👍"}}
				assert.False(t, gw.ignoreTextEmpty(msg))
				assert.Empty(t, gw.handleMessage(msg, dest), "reaction must not create a message mapping")
				want := direction.want
				if origin == "slack" && direction.to == "discord" {
					want = "root;part2"
				}
				assert.Equal(t, want, capture.message.ID)
				assert.Equal(t, msg.Reaction, capture.message.Reaction)
				capture.message = config.Message{}
				msg.ID = "unknown"
				assert.Empty(t, gw.handleMessage(msg, dest))
				assert.Empty(t, capture.message.ID, "unknown message must not be sent")
			}
		})
	}
}
