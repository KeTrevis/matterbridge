package bdiscord

import (
	"path/filepath"
	"testing"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/helper"
	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionReceivePresenceAndIgnoreEcho(t *testing.T) {
	b := threadBridge(t, false,
		discordRequest{"GET", "/channels/main/messages/msg/reactions/👍", 200, `[{"id":"bot"},{"id":"other"}]`, nil},
		discordRequest{"GET", "/channels/main/messages/msg/reactions/👍", 200, `[{"id":"bot"}]`, nil},
	)
	event := &discordgo.MessageReaction{GuildID: "guild", ChannelID: "main", MessageID: "msg", UserID: "bot", Emoji: discordgo.Emoji{Name: "👍"}}
	b.receiveReaction(event)
	assert.Empty(t, b.Remote)
	event.UserID = "person"
	for _, wantRemove := range []bool{false, true} {
		b.receiveReaction(event)
		msg := <-b.Remote
		require.NotNil(t, msg.Reaction)
		assert.Equal(t, wantRemove, msg.Reaction.Remove)
		assert.Equal(t, "ID:main", msg.Channel)
	}
	event.Emoji.ID = "custom"
	b.receiveReaction(event)
	assert.Empty(t, b.Remote)
}

func TestReactionSendInThreadAndSplitMessage(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"PUT", "/channels/root/messages/reply/reactions/👍/@me", 204, "", nil},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"DELETE", "/channels/root/messages/reply/reactions/👍/@me", 204, "", nil},
	)
	msg := config.Message{Channel: "ID:main", ID: "reply;second", ParentID: "root", Event: config.EventReaction, Reaction: &config.Reaction{Emoji: "👍"}}
	for _, remove := range []bool{false, true} {
		msg.Reaction.Remove = remove
		id, err := b.Send(msg)
		require.NoError(t, err)
		assert.Empty(t, id)
	}
}

func TestReactionReadFailureDoesNotRemoveRemoteReaction(t *testing.T) {
	b := threadBridge(t, false, discordRequest{"GET", "/channels/main/messages/msg/reactions/👍", 403, `{"code":50013,"message":"Missing Permissions"}`, nil})
	b.reactionRemove(b.c, &discordgo.MessageReactionRemove{MessageReaction: &discordgo.MessageReaction{
		GuildID: "guild", ChannelID: "main", MessageID: "msg", UserID: "person", Emoji: discordgo.Emoji{Name: "👍"},
	}})
	assert.Empty(t, b.Remote)
}

func TestReactionReceiveInThread(t *testing.T) {
	b := threadBridge(t, true, discordRequest{"GET", "/channels/root/messages/reply/reactions/👍", 200, `[{"id":"person"}]`, nil})
	require.NoError(t, b.c.State.ChannelAdd(&discordgo.Channel{ID: "root", ParentID: "main", GuildID: "guild", Type: discordgo.ChannelTypeGuildPublicThread}))
	b.reactionAdd(b.c, &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{
		GuildID: "guild", ChannelID: "root", MessageID: "reply", UserID: "person", Emoji: discordgo.Emoji{Name: "👍"},
	}})
	msg := <-b.Remote
	assert.Equal(t, "root", msg.ParentID)
	assert.Equal(t, "ID:main", msg.Channel)
	assert.False(t, msg.Reaction.Remove)
}

func TestBangBangReactionSendAndRemove(t *testing.T) {
	b := threadBridge(t, false,
		discordRequest{"PUT", "/channels/main/messages/msg/reactions/‼️/@me", 204, "", nil},
		discordRequest{"DELETE", "/channels/main/messages/msg/reactions/‼️/@me", 204, "", nil},
	)
	msg := config.Message{Channel: "ID:main", ID: "msg", Event: config.EventReaction, Reaction: &config.Reaction{Emoji: "‼️"}}
	for _, remove := range []bool{false, true} {
		msg.Reaction.Remove = remove
		_, err := b.Send(msg)
		require.NoError(t, err)
	}
}

func TestMappedCustomDiscordReaction(t *testing.T) {
	b := threadBridge(t, false, discordRequest{"GET", "/channels/main/messages/msg", 200, `{"reactions":[{"emoji":{"name":"party","id":"1234"},"count":1,"me":false}]}`, nil})
	b.reactionEmojis = helper.ReactionEmojiMap{"slack_party": "party:1234"}
	b.reactionAdd(b.c, &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{GuildID: "guild", ChannelID: "main", MessageID: "msg", UserID: "person", Emoji: discordgo.Emoji{Name: "party", ID: "1234"}}})
	require.Len(t, b.Remote, 1)
	assert.Equal(t, "party:1234", (<-b.Remote).Reaction.Emoji)
	b.reactionAdd(b.c, &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{GuildID: "guild", ChannelID: "main", MessageID: "msg", UserID: "person", Emoji: discordgo.Emoji{Name: "unknown", ID: "5678"}}})
	assert.Empty(t, b.Remote)
}

func TestDiscordConnectRejectsMissingReactionMapping(t *testing.T) {
	b := threadBridge(t, true)
	b.Config.Config.(*config.TestConfig).Overrides["discord.test.ReactionEmojiMappingFile"] = filepath.Join(t.TempDir(), "missing.json")
	require.ErrorContains(t, b.Connect(), "read reaction emoji mapping")
}

func TestDiscordMappedReactionPresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reactions string
		remove    bool
		fail      bool
	}{
		{name: "standard emoji remains", reactions: `[{"emoji":{"name":"🎉"},"count":2,"me":true}]`},
		{name: "only bot remains", reactions: `[{"emoji":{"name":"party","id":"1234"},"count":1,"me":true},{"emoji":{"name":"🎉"},"count":1,"me":true}]`, remove: true},
		{name: "last reaction removed", reactions: `[]`, remove: true},
		{name: "unrelated reaction remains", reactions: `[{"emoji":{"name":"👍"},"count":1}]`, remove: true},
		{name: "read failure", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := 200, `{"reactions":`+tc.reactions+`}`
			if tc.fail {
				status, body = 403, `{"code":50013,"message":"Missing Permissions"}`
			}
			b := threadBridge(t, false, discordRequest{"GET", "/channels/main/messages/msg", status, body, nil})
			b.reactionEmojis = helper.ReactionEmojiMap{"tada": "party:1234"}
			b.receiveReaction(&discordgo.MessageReaction{GuildID: "guild", ChannelID: "main", MessageID: "msg", UserID: "person", Emoji: discordgo.Emoji{Name: "party", ID: "1234"}})
			if tc.fail {
				assert.Empty(t, b.Remote)
				return
			}
			require.Len(t, b.Remote, 1)
			msg := <-b.Remote
			assert.Equal(t, tc.remove, msg.Reaction.Remove)
			assert.Equal(t, "tada", b.reactionEmojis.Slack(msg.Reaction.Emoji))
		})
	}
}
