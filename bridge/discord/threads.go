package bdiscord

import (
	"errors"
	"fmt"
	"strings"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/bwmarrin/discordgo"
)

const cThreadMessage = "thread_message:"

func (b *Bdiscord) nativeThreadChannel(msg *config.Message, channelID string) (string, error) {
	messageID := strings.Split(msg.ID, ";")[0]
	if target, ok := b.cache.Get(cThreadMessage + messageID); ok {
		if msg.Event != config.EventMsgDelete && msg.Event != config.EventFileDelete {
			if _, err := b.discordThread(channelID, target.(string)); err != nil {
				return "", err
			}
		}
		msg.ParentID = ""
		return target.(string), nil
	}
	if msg.ParentNotFound() {
		if msg.Event == config.EventMsgDelete || msg.Event == config.EventFileDelete {
			return channelID, nil
		}
		return "", fmt.Errorf("cannot route Discord thread: parent message is no longer in the gateway cache")
	}
	if !msg.ParentValid() {
		return channelID, nil
	}
	rootID := strings.Split(msg.ParentID, ";")[0]
	// Slack includes thread_ts on edits of the root message too.
	if messageID == rootID {
		return channelID, nil
	}
	thread, err := b.discordThread(channelID, rootID)
	if err != nil {
		return "", err
	}
	msg.ParentID = ""
	return thread.ID, nil
}

func (b *Bdiscord) discordThread(channelID, rootID string) (*discordgo.Channel, error) {
	thread, err := b.c.Channel(rootID)
	if discordErrorCode(err, discordgo.ErrCodeUnknownChannel) {
		root, rootErr := b.c.ChannelMessage(channelID, rootID)
		if rootErr != nil {
			return nil, fmt.Errorf("get Discord thread root: %w", rootErr)
		}
		name := strings.Join(strings.Fields(root.Content), " ")
		if name == "" && len(root.Attachments) > 0 {
			name = root.Attachments[0].Filename
		}
		if name == "" {
			name = "Discussion"
		}
		title := []rune(name)
		if len(title) > 100 {
			title = title[:100]
		}
		thread, err = b.c.MessageThreadStart(channelID, rootID, string(title), 1440)
		if discordErrorCode(err, discordgo.ErrCodeThreadAlreadyCreatedForThisMessage) {
			thread, err = b.c.Channel(rootID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get or create Discord thread (requires Create Public Threads and Send Messages in Threads): %w", err)
	}
	if !publicThread(thread) || thread.ParentID != channelID || thread.GuildID != b.guildID {
		return nil, fmt.Errorf("Discord thread %s does not belong to the configured public channel", rootID)
	}
	if thread.ThreadMetadata != nil {
		if thread.ThreadMetadata.Locked {
			return nil, fmt.Errorf("Discord thread %s is locked", rootID)
		}
		if thread.ThreadMetadata.Archived {
			archived := false
			thread, err = b.c.ChannelEdit(thread.ID, &discordgo.ChannelEdit{Archived: &archived})
			if err != nil {
				return nil, fmt.Errorf("unarchive Discord thread: %w", err)
			}
		}
	}
	return thread, nil
}

func discordErrorCode(err error, code int) bool {
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == code
}

func publicThread(channel *discordgo.Channel) bool {
	return channel.Type == discordgo.ChannelTypeGuildPublicThread || channel.Type == discordgo.ChannelTypeGuildNewsThread
}

func (b *Bdiscord) routeIncomingThread(msg *config.Message, channelID string) bool {
	msg.Channel = b.getChannelName(channelID)
	if !b.GetBool("UseNativeThreads") || msg.Channel != "" {
		return true
	}
	channel, err := b.c.State.Channel(channelID)
	if err != nil {
		channel, err = b.c.Channel(channelID)
	}
	if err != nil {
		b.Log.WithError(err).Warn("Could not resolve Discord thread channel")
		return false
	}
	if !publicThread(channel) || channel.GuildID != b.guildID {
		return false
	}
	msg.Channel = b.getChannelName(channel.ParentID)
	msg.ParentID = channel.ID
	return msg.Channel != ""
}
