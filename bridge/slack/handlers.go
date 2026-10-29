package bslack

import (
	"errors"
	"fmt"
	"html"
	"time"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/helper"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// ErrEventIgnored is for events that should be ignored
var ErrEventIgnored = errors.New("this event message should ignored")

func (b *Bslack) handleSlack() {
	messages := make(chan *config.Message)
	if b.GetString(incomingWebhookConfig) != "" && b.GetString(tokenConfig) == "" {
		b.Log.Debugf("Choosing webhooks based receiving")
		go b.handleMatterHook(messages)
	} else {
		b.Log.Debugf("Choosing token based receiving")
		go b.handleSlackClient(messages)
	}
	time.Sleep(time.Second)
	b.Log.Debug("Start listening for Slack messages")
	for message := range messages {
		// don't do any action on deleted/typing messages
		if message.Event != config.EventUserTyping && message.Event != config.EventMsgDelete &&
			message.Event != config.EventFileDelete {
			b.Log.Debugf("<= Sending message from %s on %s to gateway", message.Username, b.Account)
			// cleanup the message
			message.Text = b.replaceMention(message.Text)
			message.Text = b.replaceVariable(message.Text)
			message.Text = b.replaceChannel(message.Text)
			message.Text = b.replaceURL(message.Text)
			message.Text = b.replaceb0rkedMarkDown(message.Text)
			message.Text = html.UnescapeString(message.Text)

			// Add the avatar
			if message.Avatar == "" {
				message.Avatar = b.users.getAvatar(message.UserID)
			}
		}

		b.Log.Debugf("<= Message is %#v", message)
		b.Remote <- *message
	}
}

func (b *Bslack) handleSlackClient(messages chan *config.Message) {
	for msg := range b.smc.Events {
		switch msg.Type {
		case socketmode.EventTypeConnected:
			if authTest, authErr := b.smc.AuthTest(); authErr == nil {
				if botInfo, infoErr := b.smc.GetBotInfo(slack.GetBotInfoParameters{Bot: authTest.BotID}); infoErr == nil {
					b.si = botInfo

					b.channels.populateChannels(true)
					b.users.populateUsers(true)
				} else {
					b.Log.Fatalf("Unable to identify bot user")
				}
			} else {
				b.Log.Fatalf("Unable to identify bot user")
			}
		case socketmode.EventTypeConnectionError:
			ev, _ := msg.Data.(slack.ConnectionErrorEvent)

			b.Log.Errorf("Connection failed %#v %#v", ev.Error(), ev.ErrorObj)
		case socketmode.EventTypeErrorWriteFailed:
			ev, _ := msg.Data.(socketmode.ErrorWriteFailed)

			b.Log.Debugf("%#v", ev.Cause.Error())
		case socketmode.EventTypeInvalidAuth:
			ev, _ := msg.Data.(slack.InvalidAuthEvent)

			b.Log.Fatalf("Invalid Token %#v", ev)
		case socketmode.EventTypeDisconnect:
			b.Log.Info("Socket Mode connection disconnected, will reconnect automatically")
		case socketmode.EventTypeHello, socketmode.EventTypeConnecting:
			continue

		case socketmode.EventTypeEventsAPI:
			b.smc.Ack(*msg.Request)

			eventsAPIEvent, ok := msg.Data.(slackevents.EventsAPIEvent)

			if !ok {
				b.Log.Debugf("Ignored %+v", eventsAPIEvent)

				continue
			}

			switch innerEventData := eventsAPIEvent.InnerEvent.Data.(type) {
			case *slackevents.MessageEvent:
				if b.skipMessageEvent(innerEventData) {
					b.Log.Debugf("Skipped message: %#v", innerEventData)
					continue
				}
				rmsg, err := b.handleMessageEvent(innerEventData)
				if err != nil {
					b.Log.Errorf("%#v", err)
					continue
				}
				messages <- rmsg
			case *slackevents.MemberJoinedChannelEvent:
				if innerEventData.User == b.si.UserID {
					channel, err := b.smc.GetConversationInfo(&slack.GetConversationInfoInput{ChannelID: innerEventData.Channel})
					if err != nil {
						b.Log.Errorf("Unable to get conversation info for channel %s", innerEventData.Channel)
					}

					b.channels.registerChannel(*channel)
				} else {
					b.users.populateUser(innerEventData.User)
				}
			}
		default:
			b.Log.Debugf("== Receiving event %#v", msg.Data)
		}
	}
}

func (b *Bslack) handleMatterHook(messages chan *config.Message) {
	for {
		message := b.mh.Receive()
		b.Log.Debugf("receiving from matterhook (slack) %#v", message)
		if message.UserName == "slackbot" {
			continue
		}
		messages <- &config.Message{
			Username: message.UserName,
			Text:     message.Text,
			Channel:  message.ChannelName,
		}
	}
}

// skipMessageEvent skips event that need to be skipped :-)
func (b *Bslack) skipMessageEvent(ev *slackevents.MessageEvent) bool {
	switch ev.SubType {
	case sChannelLeave, sChannelJoin:
		return b.GetBool(noSendJoinConfig)
	case sPinnedItem, sUnpinnedItem:
		return true
	case sChannelTopic, sChannelPurpose:
		// Skip the event if our bot/user account changed the topic/purpose
		if ev.BotID == b.si.ID {
			return true
		}
	}

	// Check for our callback ID in blocks.
	// For Socket Mode / Events API, blocks are at the top level (ev.Blocks),
	// not inside attachments. Check both locations for compatibility.
	// Iterate ALL blocks since Slack can inject additional blocks (e.g. rich_text)
	// before our SectionBlock.
	hasOurCallbackID := false
	callbackBlockID := "matterbridge_" + b.uuid

	// Check top-level blocks (Socket Mode / Events API)
	for _, blk := range ev.Blocks.BlockSet {
		if section, ok := blk.(*slack.SectionBlock); ok && section.BlockID == callbackBlockID {
			hasOurCallbackID = true
			break
		}
	}
	// Note: attachment blocks are only checked via ev.Message.Attachments below.
	// MessageEvent no longer carries its own top-level Attachments field; since
	// ev.Message is now always populated (synthesized from the top-level fields
	// for regular messages, or the real nested object for message_changed), the
	// ev.Message.Attachments check below covers both cases uniformly.

	if ev.Message != nil {
		// Only message_changed can be an unfurl; regular replies also have a synthesized Message.
		if ev.SubType == sMessageChanged && ev.Message.ThreadTimestamp != ev.Message.Timestamp &&
			ev.Message.Edited == nil {
			return true
		}

		// Check top-level blocks on inner message (message_changed events via Socket Mode)
		if !hasOurCallbackID {
			for _, blk := range ev.Message.Blocks.BlockSet {
				if section, ok := blk.(*slack.SectionBlock); ok && section.BlockID == callbackBlockID {
					hasOurCallbackID = true
					break
				}
			}
		}
		// Check blocks inside attachments on inner message (legacy / RTM)
		if !hasOurCallbackID {
			for _, att := range ev.Message.Attachments {
				for _, blk := range att.Blocks.BlockSet {
					if section, ok := blk.(*slack.SectionBlock); ok && section.BlockID == callbackBlockID {
						hasOurCallbackID = true
						break
					}
				}
				if hasOurCallbackID {
					break
				}
			}
		}
	}

	// Skip any messages that we made ourselves or from 'slackbot' (see #527).
	// For message_changed events, the bot ID is on ev.Message.BotID, not ev.BotID.
	// Also check ev.User against the bot's UserID, as the BotID field may not always
	// match b.si.ID depending on the Slack app configuration.
	if ev.Username == sSlackBotUser ||
		(b.smc != nil && ev.BotID == b.si.ID) ||
		(b.smc != nil && ev.User == b.si.UserID) ||
		(b.smc != nil && ev.Message != nil && ev.Message.BotID == b.si.ID) ||
		hasOurCallbackID {
		return true
	}

	if ev.Message != nil && len(ev.Message.Files) > 0 {
		return b.filesCached(ev.Message.Files)
	}
	return false
}

func (b *Bslack) filesCached(files []slack.File) bool {
	for i := range files {
		if !b.fileCached(&files[i]) {
			return false
		}
	}
	return true
}

// handleMessageEvent handles the message events. Together with any called sub-methods,
// this method implements the following event processing pipeline:
//
//  1. Check if the message should be ignored.
//     NOTE: This is not actually part of the method below but is done just before it
//     is called via the 'skipMessageEvent()' method.
//  2. Populate the Matterbridge message that will be sent to the router based on the
//     received event and logic that is common to all events that are not skipped.
//  3. Detect and handle any message that is "status" related (think join channel, etc.).
//     This might result in an early exit from the pipeline and passing of the
//     pre-populated message to the Matterbridge router.
//  4. Handle the specific case of messages that edit existing messages depending on
//     configuration.
//  5. Handle any attachments of the received event.
//  6. Check that the Matterbridge message that we end up with after at the end of the
//     pipeline is valid before sending it to the Matterbridge router.
func (b *Bslack) handleMessageEvent(ev *slackevents.MessageEvent) (*config.Message, error) {
	rmsg, err := b.populateReceivedMessage(ev)
	if err != nil {
		return nil, err
	}

	// Handle some message types early.
	if b.handleStatusEvent(ev, rmsg) {
		return rmsg, nil
	}

	b.handleAttachments(ev, rmsg)

	// Verify that we have the right information and the message
	// is well-formed before sending it out to the router.
	if (ev.Message == nil || len(ev.Message.Files) == 0) && (rmsg.Text == "" || rmsg.Username == "") {
		if ev.BotID != "" {
			// This is probably a webhook we couldn't resolve.
			return nil, fmt.Errorf("message handling resulted in an empty bot message (probably an incoming webhook we couldn't resolve): %#v", ev)
		}
		if ev.Message != nil {
			return nil, fmt.Errorf("message handling resulted in an empty message: %#v with submessage %#v", ev, ev.Message)
		}
		return nil, fmt.Errorf("message handling resulted in an empty message: %#v", ev)
	}
	return rmsg, nil
}

func (b *Bslack) handleStatusEvent(ev *slackevents.MessageEvent, rmsg *config.Message) bool {
	switch ev.SubType {
	case sChannelJoined, sMemberJoined:
		// There's no further processing needed on channel events
		// so we return 'true'.
		return true
	case sChannelJoin, sChannelLeave:
		rmsg.Username = sSystemUser
		rmsg.Event = config.EventJoinLeave
	case sChannelTopic, sChannelPurpose:
		b.channels.populateChannels(false)
		rmsg.Event = config.EventTopicChange
	case sMessageChanged:
		rmsg.Text = ev.Message.Text
		// handle deleted thread starting messages
		if ev.Message.Text == "This message was deleted." {
			rmsg.Event = config.EventMsgDelete
			return true
		}
	case sMessageDeleted:
		rmsg.Text = config.EventMsgDelete
		rmsg.Event = config.EventMsgDelete
		rmsg.ID = ev.PreviousMessage.Timestamp
		// If a message is being deleted we do not need to process
		// the event any further so we return 'true'.
		return true
	case sMeMessage:
		rmsg.Event = config.EventUserAction
	}
	return false
}

func getMessageTitle(attach *slack.Attachment) string {
	if attach.TitleLink != "" {
		return fmt.Sprintf("[%s](%s)\n", attach.Title, attach.TitleLink)
	}
	return attach.Title
}

func (b *Bslack) handleAttachments(ev *slackevents.MessageEvent, rmsg *config.Message) {
	// File comments are set by the system (because there is no username given).
	if ev.SubType == sFileComment {
		rmsg.Username = sSystemUser
	}

	if ev.Message == nil {
		return
	}

	// See if we have some text in the attachments.
	if rmsg.Text == "" {
		for i, attach := range ev.Message.Attachments {
			if attach.Text != "" {
				if attach.Title != "" {
					rmsg.Text = getMessageTitle(&ev.Message.Attachments[i])
				}
				rmsg.Text += attach.Text
				if attach.Footer != "" {
					rmsg.Text += "\n\n" + attach.Footer
				}
			} else {
				rmsg.Text = attach.Fallback
			}
		}
	}

	// Save the attachments, so that we can send them to other slack (compatible) bridges.
	if len(ev.Message.Attachments) > 0 {
		rmsg.Extra[sSlackAttachment] = append(rmsg.Extra[sSlackAttachment], ev.Message.Attachments)
	}

	// If we have files attached, download them (in memory) and put a pointer to it in msg.Extra.
	for i := range ev.Message.Files {
		// keep reference in cache on which channel we added this file
		b.cache.Add(cfileDownloadChannel+ev.Message.Files[i].ID, ev.Channel)

		err := b.handleDownloadFile(rmsg, &ev.Message.Files[i], false)
		if err != nil {
			b.Log.Errorf("Could not download incoming file: %#v", err)
		}
	}
}

// handleDownloadFile handles file download
func (b *Bslack) handleDownloadFile(rmsg *config.Message, file *slack.File, retry bool) error {
	if b.fileCached(file) {
		return nil
	}
	// Check that the file is neither too large nor blacklisted.
	if err := helper.HandleDownloadSize(b.Log, rmsg, file.Name, int64(file.Size), b.General); err != nil {
		b.Log.WithError(err).Infof("Skipping download of incoming file.")
		return nil
	}

	// Actually download the file.
	url := file.URLPrivateDownload
	if url == "" {
		url = file.URLPrivate
	}
	data, err := helper.DownloadFileAuth(url, "Bearer "+b.GetString(tokenConfig))
	if err != nil {
		return fmt.Errorf("download %s failed %#v", url, err)
	}

	if len(*data) != file.Size && !retry {
		b.Log.Debugf("Data size (%d) is not equal to size declared (%d)\n", len(*data), file.Size)
		time.Sleep(1 * time.Second)
		return b.handleDownloadFile(rmsg, file, true)
	}

	// If a comment is attached to the file(s) it is in the 'Text' field of the Slack messge event
	// and should be added as comment to only one of the files. We reset the 'Text' field to ensure
	// that the comment is not duplicated.
	comment := rmsg.Text
	rmsg.Text = ""
	helper.HandleDownloadData2(b.Log, rmsg, file.Name, file.ID, comment, url, data, b.General)
	return nil
}

// handleGetChannelMembers handles messages containing the GetChannelMembers event
// Sends a message to the router containing *config.ChannelMembers
func (b *Bslack) handleGetChannelMembers(rmsg *config.Message) bool {
	if rmsg.Event != config.EventGetChannelMembers {
		return false
	}

	cMembers := b.channels.getChannelMembers(b.users)

	extra := make(map[string][]interface{})
	extra[config.EventGetChannelMembers] = append(extra[config.EventGetChannelMembers], cMembers)
	msg := config.Message{
		Extra:   extra,
		Event:   config.EventGetChannelMembers,
		Account: b.Account,
	}

	b.Log.Debugf("sending msg to remote %#v", msg)
	b.Remote <- msg

	return true
}

// fileCached implements Matterbridge's caching logic for files
// shared via Slack.
//
// We consider that a file was cached if its ID was added in the last minute or
// it's name was registered in the last 10 seconds. This ensures that an
// identically named file but with different content will be uploaded correctly
// (the assumption is that such name collisions will not occur within the given
// timeframes).
func (b *Bslack) fileCached(file *slack.File) bool {
	if ts, ok := b.cache.Get("file" + file.ID); ok && time.Since(ts.(time.Time)) < time.Minute {
		return true
	} else if ts, ok = b.cache.Get("filename" + file.Name); ok && time.Since(ts.(time.Time)) < 10*time.Second {
		return true
	}
	return false
}
