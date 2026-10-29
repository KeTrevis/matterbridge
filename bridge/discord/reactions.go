package bdiscord

import (
	"strings"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/bwmarrin/discordgo"
)

func (b *Bdiscord) reactionAdd(_ *discordgo.Session, event *discordgo.MessageReactionAdd) {
	b.receiveReaction(event.MessageReaction)
}

func (b *Bdiscord) reactionRemove(_ *discordgo.Session, event *discordgo.MessageReactionRemove) {
	b.receiveReaction(event.MessageReaction)
}

func (b *Bdiscord) receiveReaction(event *discordgo.MessageReaction) {
	// shortcut: custom emoji require an explicit cross-platform mapping when needed.
	if event.GuildID != b.guildID || event.UserID == b.userID || event.Emoji.ID != "" {
		return
	}
	// Serialize snapshots so concurrent gateway callbacks cannot relay stale counts last.
	b.reactionsMutex.Lock()
	defer b.reactionsMutex.Unlock()
	msg := config.Message{Account: b.Account, ID: event.MessageID, UserID: event.UserID, Event: config.EventReaction}
	if !b.routeIncomingThread(&msg, event.ChannelID) || msg.Channel == "" {
		return
	}
	// Two users suffice: at most one can be the bridge's own bot.
	users, err := b.c.MessageReactions(event.ChannelID, event.MessageID, event.Emoji.Name, 2, "", "")
	if err != nil {
		b.Log.WithError(err).Warn("Could not read Discord reactions")
		return
	}
	active := false
	for _, user := range users {
		if user.ID != b.userID {
			active = true
		}
	}
	msg.Reaction = &config.Reaction{Emoji: event.Emoji.Name, Remove: !active}
	b.Remote <- msg
}

func (b *Bdiscord) sendReaction(msg config.Message, channelID string) error {
	if msg.ID == "" || msg.Reaction == nil || msg.Reaction.Emoji == "" {
		return nil
	}
	// A long Slack message may be split; attach its reactions to the first part.
	id := strings.Split(msg.ID, ";")[0]
	if msg.Reaction.Remove {
		return b.c.MessageReactionRemove(channelID, id, msg.Reaction.Emoji, "@me")
	}
	return b.c.MessageReactionAdd(channelID, id, msg.Reaction.Emoji)
}
