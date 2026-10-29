package bslack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/helper"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionEmojiConversion(t *testing.T) {
	b := &Bslack{reactionEmojis: helper.ReactionEmojiMap{"brilliant": "‼️"}}
	for _, name := range []string{"+1", "heart", "tada", "thumbsup::skin-tone-3", "flag-fr", "brilliant", "bangbang"} {
		value := b.reactionEmojis.Discord(name)
		require.NotEmpty(t, value)
		assert.Equal(t, value, b.reactionEmojis.Discord(b.reactionEmojis.Slack(value)))
	}
	assert.Equal(t, "heart", b.reactionEmojis.Slack("❤"))
	assert.Equal(t, "‼️", b.reactionEmojis.Discord("brilliant"))
	assert.Equal(t, "brilliant", b.reactionEmojis.Slack("‼️"))
	assert.Equal(t, "brilliant", b.reactionEmojis.Slack("‼"))
	assert.Empty(t, b.reactionEmojis.Discord("custom_company_emoji"))
	assert.Empty(t, b.reactionEmojis.Slack("custom:1234"))
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

func TestBrilliantReactionPresenceAndAlias(t *testing.T) {
	for _, tc := range []struct {
		name, eventName string
		reactions       []slack.ItemReaction
		remove          bool
	}{
		{name: "add brilliant", eventName: "brilliant", reactions: []slack.ItemReaction{{Name: "brilliant", Count: 1, Users: []string{"other"}}}},
		{name: "remove last brilliant", eventName: "brilliant", reactions: []slack.ItemReaction{{Name: "brilliant", Count: 1, Users: []string{"bot"}}}, remove: true},
		{name: "standard bangbang alias", eventName: "bangbang", reactions: []slack.ItemReaction{{Name: "bangbang", Count: 1, Users: []string{"other"}}}},
		{name: "removing brilliant keeps standard reaction", eventName: "brilliant", reactions: []slack.ItemReaction{{Name: "bangbang", Count: 1, Users: []string{"other"}}, {Name: "brilliant", Count: 1, Users: []string{"bot"}}}},
		{name: "removing standard keeps brilliant", eventName: "bangbang", reactions: []slack.ItemReaction{{Name: "brilliant", Count: 1, Users: []string{"other"}}, {Name: "bangbang", Count: 1, Users: []string{"bot"}}}},
		{name: "only bot reactions remain", eventName: "brilliant", reactions: []slack.ItemReaction{{Name: "brilliant", Count: 1, Users: []string{"bot"}}, {Name: "bangbang", Count: 1, Users: []string{"bot"}}}, remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "channel", r.FormValue("channel"))
				assert.Equal(t, "2.0", r.FormValue("timestamp"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/reactions.get" {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "type": "message", "channel": "channel", "message": map[string]interface{}{"ts": "2.0", "thread_ts": "1.0", "reactions": tc.reactions}})
				} else {
					path := "/reactions.add"
					if tc.remove {
						path = "/reactions.remove"
					}
					assert.Equal(t, path, r.URL.Path)
					assert.Equal(t, "brilliant", r.FormValue("name"))
					_, _ = io.WriteString(w, `{"ok":true}`)
				}
			}))
			defer server.Close()
			b := identityTestBridge(server.URL, false)
			b.reactionEmojis = helper.ReactionEmojiMap{"brilliant": "‼️"}
			b.si = &slack.Bot{UserID: "bot"}
			messages := make(chan *config.Message, 1)
			b.receiveReaction("other", tc.eventName, slackevents.Item{Type: "message", Channel: "channel", Timestamp: "2.0"}, messages)
			require.Len(t, messages, 1)
			msg := <-messages
			assert.Equal(t, "‼️", msg.Reaction.Emoji)
			assert.Equal(t, tc.remove, msg.Reaction.Remove)
			assert.Equal(t, "1.0", msg.ParentID)
			require.NoError(t, b.sendReaction(*msg))
			assert.Equal(t, 2, calls)
		})
	}
}

func TestReactionMappingWithoutCodeChange(t *testing.T) {
	b := &Bslack{reactionEmojis: helper.ReactionEmojiMap{"slack_party": "party:1234"}}
	assert.Equal(t, "party:1234", b.reactionEmojis.Discord("slack_party"))
	assert.Equal(t, "slack_party", b.reactionEmojis.Slack("party:1234"))
	assert.Equal(t, "👍", b.reactionEmojis.Discord("+1"))
	assert.Equal(t, "+1", b.reactionEmojis.Slack("👍"))
	b.reactionEmojis = helper.ReactionEmojiMap{}
	assert.Empty(t, b.reactionEmojis.Discord("brilliant"), "no custom mapping may be hardcoded")
	assert.Equal(t, "bangbang", b.reactionEmojis.Slack("‼️"))
}

func TestSlackConnectRejectsMissingReactionMapping(t *testing.T) {
	b := identityTestBridge("http://127.0.0.1:1", false)
	b.Config.Config = &config.TestConfig{Config: b.Config.Config, Overrides: map[string]interface{}{"slack.test.ReactionEmojiMappingFile": filepath.Join(t.TempDir(), "missing.json")}}
	require.ErrorContains(t, b.Connect(), "read reaction emoji mapping")
}
