package bslack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionEmojiConversion(t *testing.T) {
	for _, name := range []string{"+1", "heart", "tada", "thumbsup::skin-tone-3", "flag-fr"} {
		value := reactionUnicode(name)
		require.NotEmpty(t, value)
		assert.Equal(t, value, reactionUnicode(reactionName(value)))
	}
	assert.Equal(t, "heart", reactionName("❤"))
	assert.Empty(t, reactionUnicode("custom_company_emoji"))
	assert.Empty(t, reactionName("custom:1234"))
}

func TestReactionReceiveAndSend(t *testing.T) {
	count := 2
	fail := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "channel", r.FormValue("channel"))
		assert.Equal(t, "2.0", r.FormValue("timestamp"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/reactions.get" {
			if fail {
				_, _ = io.WriteString(w, `{"ok":false,"error":"missing_scope"}`)
				return
			}
			if count == 2 {
				_, _ = io.WriteString(w, `{"ok":true,"type":"message","channel":"channel","message":{"ts":"2.0","thread_ts":"1.0","reactions":[{"name":"+1","count":2,"users":["bot","other"]}]}}`)
			} else {
				_, _ = io.WriteString(w, `{"ok":true,"type":"message","channel":"channel","message":{"ts":"2.0","thread_ts":"1.0","reactions":[{"name":"+1","count":1,"users":["bot"]}]}}`)
			}
		} else {
			if count == 2 {
				assert.Equal(t, "/reactions.add", r.URL.Path)
			} else {
				assert.Equal(t, "/reactions.remove", r.URL.Path)
			}
			assert.Equal(t, "+1", r.FormValue("name"))
			if count == 2 {
				_, _ = io.WriteString(w, `{"ok":false,"error":"already_reacted"}`)
			} else {
				_, _ = io.WriteString(w, `{"ok":false,"error":"no_reaction"}`)
			}
		}
	}))
	defer server.Close()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "slack.test"})
	br.Config = config.NewConfigFromString(logger, []byte("[slack.test]\n"))
	br.Log = logrus.NewEntry(logger)
	b := newBridge(&bridge.Config{Bridge: br})
	b.sc = slack.New("test", slack.OptionAPIURL(server.URL+"/"))
	b.si = &slack.Bot{UserID: "bot"}
	b.channels = newChannelManager(br.Log, b.sc)
	b.channels.registerChannel(slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "channel"}, Name: "general"}})
	messages := make(chan *config.Message, 2)
	item := slackevents.Item{Type: "message", Channel: "channel", Timestamp: "2.0"}
	b.receiveReaction("bot", "+1", item, messages)
	assert.Zero(t, calls, "own reactions must not loop")
	for _, wantRemove := range []bool{false, true} {
		b.receiveReaction("person", "+1", item, messages)
		msg := <-messages
		assert.Equal(t, wantRemove, msg.Reaction.Remove)
		assert.Equal(t, "1.0", msg.ParentID)
		assert.Equal(t, "general", msg.Channel)
		require.NoError(t, b.sendReaction(*msg))
		count = 1
	}
	assert.Equal(t, 4, calls)
	fail = true
	b.receiveReaction("person", "+1", item, messages)
	assert.Empty(t, messages, "read failures must not remove the remote reaction")
}
