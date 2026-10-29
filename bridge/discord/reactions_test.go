package bdiscord

import (
	"testing"

	"github.com/42wim/matterbridge/bridge/config"
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
