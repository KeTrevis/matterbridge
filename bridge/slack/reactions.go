package bslack

import (
	"strings"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func (b *Bslack) receiveReaction(user, name string, item slackevents.Item, messages chan *config.Message) {
	if b.si == nil || user == b.si.UserID || item.Type != "message" {
		return
	}
	value := b.reactionEmojis.Discord(name)
	if value == "" {
		return
	}
	channel, err := b.channels.getChannelByID(item.Channel)
	if err != nil {
		b.Log.WithError(err).Warn("Could not resolve Slack reaction channel")
		return
	}
	ref := slack.ItemRef{Channel: item.Channel, Timestamp: item.Timestamp}
	current, err := b.sc.GetReactions(ref, slack.GetReactionsParameters{Full: true})
	if err != nil {
		b.Log.WithError(err).Warn("Could not read Slack reactions")
		return
	}
	active := false
	for _, reaction := range current.Reactions {
		if strings.ReplaceAll(b.reactionEmojis.Discord(reaction.Name), "\ufe0f", "") != strings.ReplaceAll(value, "\ufe0f", "") {
			continue
		}
		count := reaction.Count
		for _, id := range reaction.Users {
			if id == b.si.UserID {
				count--
			}
		}
		active = active || count > 0
	}
	msg := &config.Message{Account: b.Account, Channel: channel.Name, ID: item.Timestamp,
		Event: config.EventReaction, UserID: user, Reaction: &config.Reaction{Emoji: value, Remove: !active}}
	if b.useChannelID {
		msg.Channel = "ID:" + channel.ID
	}
	if current.Message != nil && current.Message.ThreadTimestamp != item.Timestamp {
		msg.ParentID = current.Message.ThreadTimestamp
	}
	messages <- msg
}

func (b *Bslack) sendReaction(msg config.Message) error {
	if msg.ID == "" || msg.Reaction == nil || b.sc == nil {
		return nil
	}
	name := b.reactionEmojis.Slack(msg.Reaction.Emoji)
	if name == "" {
		return nil
	}
	channel, err := b.channels.getChannel(msg.Channel)
	if err != nil {
		return err
	}
	ref := slack.ItemRef{Channel: channel.ID, Timestamp: msg.ID}
	if msg.Reaction.Remove {
		err = b.sc.RemoveReaction(name, ref)
	} else {
		err = b.sc.AddReaction(name, ref)
	}
	if err != nil && (err.Error() == "already_reacted" || err.Error() == "no_reaction") {
		return nil
	}
	return err
}
