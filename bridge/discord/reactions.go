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
	if event.GuildID != b.guildID || event.UserID == b.userID {
		return
	}
	emoji := event.Emoji.APIName()
	if event.Emoji.ID != "" && b.reactionEmojis.Slack(emoji) == "" {
		return
	}
	// Serialize snapshots so concurrent gateway callbacks cannot relay stale counts last.
	b.reactionsMutex.Lock()
	defer b.reactionsMutex.Unlock()
	msg := config.Message{Account: b.Account, ID: event.MessageID, UserID: event.UserID, Event: config.EventReaction}
	if !b.routeIncomingThread(&msg, event.ChannelID) || msg.Channel == "" {
		return
	}
	active := false
	if len(b.reactionEmojis) > 0 {
		// Several Discord emoji can target one Slack reaction, including standard aliases.
		current, err := b.c.ChannelMessage(event.ChannelID, event.MessageID)
		if err != nil {
			b.Log.WithError(err).Warn("Could not read Discord reactions")
			return
		}
		target := b.reactionEmojis.Slack(emoji)
		for _, reaction := range current.Reactions {
			if b.reactionEmojis.Slack(reaction.Emoji.APIName()) != target {
				continue
			}
			count := reaction.Count
			if reaction.Me {
				count--
			}
			active = active || count > 0
		}
	} else {
		// Two users suffice: at most one can be the bridge's own bot.
		users, err := b.c.MessageReactions(event.ChannelID, event.MessageID, emoji, 2, "", "")
		if err != nil {
			b.Log.WithError(err).Warn("Could not read Discord reactions")
			return
		}
		for _, user := range users {
			active = active || user.ID != b.userID
		}
	}
	msg.Reaction = &config.Reaction{Emoji: emoji, Remove: !active}
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
