package bslack

import (
	"strings"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/kyokomi/emoji/v2"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func reactionUnicode(name string) string {
	var result string
	for _, part := range strings.Split(name, "::") {
		value, ok := emoji.CodeMap()[":"+strings.Replace(part, "flag-", "flag_", 1)+":"]
		if !ok {
			return ""
		}
		result += value
	}
	return result
}

func reactionName(value string) string {
	runes := []rune(value)
	if len(runes) == 2 && runes[0] >= 0x1f1e6 && runes[0] <= 0x1f1ff && runes[1] >= 0x1f1e6 && runes[1] <= 0x1f1ff {
		return "flag-" + string([]rune{'a' + runes[0] - 0x1f1e6, 'a' + runes[1] - 0x1f1e6})
	}
	if names := emoji.RevCodeMap()[value+"\ufe0f"]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	if names := emoji.RevCodeMap()[value]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	if names := emoji.RevCodeMap()[strings.ReplaceAll(value, "\ufe0f", "")]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	// Slack represents skin tones as a separate shortcode suffix.
	if len(runes) > 1 && runes[len(runes)-1] >= 0x1f3fb && runes[len(runes)-1] <= 0x1f3ff {
		base := reactionName(string(runes[:len(runes)-1]))
		tone := reactionName(string(runes[len(runes)-1]))
		if base != "" && tone != "" {
			return base + "::" + tone
		}
	}
	return ""
}

func (b *Bslack) receiveReaction(user, name string, item slackevents.Item, messages chan *config.Message) {
	if b.si == nil || user == b.si.UserID || item.Type != "message" {
		return
	}
	value := reactionUnicode(name)
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
		if reaction.Name != name {
			continue
		}
		count := reaction.Count
		for _, id := range reaction.Users {
			if id == b.si.UserID {
				count--
			}
		}
		active = count > 0
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
	name := reactionName(msg.Reaction.Emoji)
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
