package bslack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomSenderIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat.postMessage", r.URL.Path)
		assert.Equal(t, "Alice [Discord]", r.FormValue("username"))
		assert.Equal(t, "https://example.com/alice.png", r.FormValue("icon_url"))
		assert.NotEqual(t, "true", r.FormValue("as_user"))
		assert.Equal(t, "hello", r.FormValue("text"))
		assert.Equal(t, "1.0", r.FormValue("thread_ts"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"channel":"channel","ts":"2.0"}`)
	}))
	defer server.Close()
	b := identityTestBridge(server.URL, false)
	msg := config.Message{Channel: "ID:channel", Account: "discord.test", Username: "Alice [Discord]", Avatar: "https://example.com/alice.png", Text: "hello", ParentID: "1.0"}
	_, err := b.Send(msg)
	require.NoError(t, err)
}

func identityTestBridge(endpoint string, prefix bool) *Bslack {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "slack.test"})
	br.Config = config.NewConfigFromString(logger, []byte(fmt.Sprintf("[slack.test]\nPrefixMessagesWithNick=%t\nIconURL=\"https://example.com/fallback.png\"\n", prefix)))
	br.Log = logrus.NewEntry(logger)
	br.General = &config.Protocol{MediaDownloadSize: 10}
	b := newBridge(&bridge.Config{Bridge: br})
	b.sc = slack.New("test-token", slack.OptionAPIURL(endpoint+"/"))
	b.smc = socketmode.New(b.sc)
	b.channels = newChannelManager(b.Log, b.sc)
	b.channels.registerChannel(slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "channel"}, Name: "test"}})
	return b
}

func TestFileSenderIdentityVariants(t *testing.T) {
	for _, tc := range []struct {
		name, text, comment, parent, avatar, wantComment string
		files                                            int
		prefix                                           bool
	}{
		{name: "file only", files: 1},
		{name: "caption", files: 1, text: "caption", wantComment: "caption"},
		{name: "file comment", files: 1, comment: "comment", wantComment: "comment"},
		{name: "caption and comment", files: 1, text: "caption", comment: "comment", wantComment: "caption\ncomment"},
		{name: "duplicate caption", files: 1, text: "caption", comment: "caption", wantComment: "caption"},
		{name: "thread with avatar", files: 1, parent: "1.0", avatar: "https://example.com/alice.png"},
		{name: "multiple files", files: 2, text: "caption", wantComment: "caption"},
		{name: "legacy prefix", files: 1, prefix: true, text: "caption", wantComment: "File from Alice [Discord]\ncaption"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var endpoint string
			completed := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files.getUploadURLExternal":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "file_id": "file", "upload_url": endpoint + "/upload"})
				case "/upload":
					_, _ = io.WriteString(w, `{}`)
				case "/files.completeUploadExternal":
					wantComment := tc.wantComment
					if completed > 0 {
						wantComment = ""
					}
					assert.Equal(t, wantComment, r.FormValue("initial_comment"))
					assert.Equal(t, tc.parent, r.FormValue("thread_ts"))
					if tc.prefix {
						assert.Empty(t, r.FormValue("username"))
						assert.Empty(t, r.FormValue("icon_url"))
					} else {
						assert.Equal(t, "Alice [Discord]", r.FormValue("username"))
						avatar := tc.avatar
						if avatar == "" {
							avatar = "https://example.com/fallback.png"
						}
						assert.Equal(t, avatar, r.FormValue("icon_url"))
					}
					completed++
					_, _ = io.WriteString(w, `{"ok":true,"files":[{"id":"file"}]}`)
				case "/files.info":
					_, _ = io.WriteString(w, `{"ok":true,"file":{"id":"file","shares":{"public":{"channel":[{"ts":"2.0"}]}}}}`)
				default:
					t.Errorf("unexpected Slack request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			endpoint = server.URL
			b := identityTestBridge(endpoint, tc.prefix)
			data := []byte("file bytes")
			files := []interface{}{}
			for i := 0; i < tc.files; i++ {
				files = append(files, config.FileInfo{Name: fmt.Sprintf("file%d.png", i), Comment: tc.comment, Data: &data})
			}
			_, err := b.uploadFile(&config.Message{Account: "discord.test", Username: "Alice [Discord]", Avatar: tc.avatar, Text: tc.text, ParentID: tc.parent, Extra: map[string][]interface{}{"file": files}}, "channel")
			require.NoError(t, err)
			assert.Equal(t, tc.files, completed)
		})
	}
}

func TestSenderIdentityWithExtras(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		extra      map[string][]interface{}
	}{
		{name: "metadata", text: "hello", extra: map[string][]interface{}{"metadata": {true}}},
		{name: "attachment with text", text: "hello", extra: map[string][]interface{}{"attachments": {map[string]interface{}{"text": "preview"}}}},
		{name: "attachment without text", extra: map[string][]interface{}{"attachments": {map[string]interface{}{"text": "preview"}}}},
		{name: "native Slack attachment", extra: map[string][]interface{}{sSlackAttachment: {[]slack.Attachment{{Text: "preview"}}}}},
		{name: "oversize warning", extra: map[string][]interface{}{config.EventFileFailureSize: {config.FileInfo{Name: "large.png", Size: 99}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "/chat.postMessage", r.URL.Path)
				assert.Equal(t, "Alice [Discord]", r.FormValue("username"))
				assert.Equal(t, "https://example.com/alice.png", r.FormValue("icon_url"))
				assert.Equal(t, "1.0", r.FormValue("thread_ts"))
				if tc.name == "oversize warning" {
					assert.Contains(t, r.FormValue("text"), "large.png")
					assert.NotContains(t, r.FormValue("text"), "<system>")
				} else {
					assert.Equal(t, tc.text, r.FormValue("text"))
					if tc.text == "" {
						assert.Empty(t, r.FormValue("blocks"))
						assert.Contains(t, r.FormValue("attachments"), "preview")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true,"channel":"channel","ts":"2.0"}`)
			}))
			defer server.Close()
			b := identityTestBridge(server.URL, false)
			_, err := b.Send(config.Message{Channel: "ID:channel", Account: "discord.test", Username: "Alice [Discord]", Avatar: "https://example.com/alice.png", Text: tc.text, ParentID: "1.0", Extra: tc.extra})
			require.NoError(t, err)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestSlackBotSenderIdentity(t *testing.T) {
	for _, tc := range []struct{ name, payload, wantName, wantAvatar string }{
		{"default bot", `{"bot_id":"B1","user":"U1"}`, "Build Bot", "https://example.com/bot.png"},
		{"custom bot", `{"bot_id":"B1","user":"U1","username":"Alice","icons":{"icon_url":"https://example.com/alice.png"}}`, "Alice", "https://example.com/alice.png"},
		{"edited bot", `{"subtype":"message_changed","message":{"bot_id":"B1","user":"U1","username":"Alice","icons":{"icon_url":"https://example.com/alice.png"}}}`, "Alice", "https://example.com/alice.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/bots.info", r.URL.Path)
				assert.Equal(t, "B1", r.FormValue("bot"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true,"bot":{"id":"B1","name":"Build Bot","icons":{"image_48":"https://example.com/bot.png"}}}`)
			}))
			defer server.Close()
			b := identityTestBridge(server.URL, false)
			var ev slackevents.MessageEvent
			require.NoError(t, json.NewDecoder(strings.NewReader(tc.payload)).Decode(&ev))
			var msg config.Message
			require.NoError(t, b.populateMessageWithUserInfo(&ev, &msg))
			assert.Equal(t, tc.wantName, msg.Username)
			assert.Equal(t, tc.wantAvatar, msg.Avatar)
		})
	}
}

func TestSlackEditAndDeletePreserveSender(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		assert.Equal(t, "channel", r.FormValue("channel"))
		switch r.URL.Path {
		case "/chat.postMessage", "/chat.update":
			assert.Equal(t, "Alice [Discord]", r.FormValue("username"))
			assert.Equal(t, "https://example.com/alice.png", r.FormValue("icon_url"))
			assert.NotEqual(t, "true", r.FormValue("as_user"))
			assert.Equal(t, "1.0", r.FormValue("thread_ts"))
			if r.URL.Path == "/chat.update" {
				assert.Equal(t, "2.0", r.FormValue("ts"))
				assert.Equal(t, "edited", r.FormValue("text"))
			}
		case "/chat.delete":
			assert.Equal(t, "2.0", r.FormValue("ts"))
		default:
			t.Errorf("unexpected Slack request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"channel":"channel","ts":"2.0"}`)
	}))
	defer server.Close()
	b := identityTestBridge(server.URL, false)
	msg := config.Message{Channel: "ID:channel", Account: "discord.test", Username: "Alice [Discord]", Avatar: "https://example.com/alice.png", Text: "hello", ParentID: "1.0"}
	id, err := b.Send(msg)
	require.NoError(t, err)
	msg.ID, msg.Text = id, "edited"
	_, err = b.Send(msg)
	require.NoError(t, err)
	msg.Event = config.EventMsgDelete
	_, err = b.Send(msg)
	require.NoError(t, err)
	assert.Equal(t, []string{"/chat.postMessage", "/chat.update", "/chat.delete"}, calls)
}

func TestCustomFileIdentityDoesNotEcho(t *testing.T) {
	b := identityTestBridge("http://127.0.0.1:1", false)
	b.si = &slack.Bot{ID: "Bbridge", UserID: "Ubridge"}
	for _, payload := range []string{
		`{"type":"message","subtype":"file_share","bot_id":"Bbridge","username":"Alice [Discord]","files":[{"id":"F1"}]}`,
		`{"type":"message","subtype":"file_share","user":"Ubridge","username":"Alice [Discord]","files":[{"id":"F1"}]}`,
		`{"type":"message","subtype":"message_changed","message":{"bot_id":"Bbridge","username":"Alice [Discord]","files":[{"id":"F1"}]}}`,
	} {
		var event slackevents.MessageEvent
		require.NoError(t, json.Unmarshal([]byte(payload), &event))
		assert.True(t, b.skipMessageEvent(&event), "our custom author must not bypass loop prevention")
	}
}
