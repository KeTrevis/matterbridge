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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A regular top-level message as delivered by the Events API / Socket
// Mode: no "thread_ts" key at all (that's only present on replies), no
// "message" key (that's only present on message_changed/message_deleted).
const regularTopLevelMessageJSON = `{
	"type": "message",
	"user": "U123HUMAN",
	"text": "hello",
	"ts": "1786248460.671289",
	"channel": "C0APH6R72HE",
	"channel_type": "channel",
	"event_ts": "1786248460.671289"
}`

func TestMessageEventUnmarshalSynthesizesThreadTimestamp(t *testing.T) {
	var ev slackevents.MessageEvent
	require.NoError(t, json.Unmarshal([]byte(regularTopLevelMessageJSON), &ev))

	require.NotNil(t, ev.Message, "Message should be synthesized for a regular top-level message")
	assert.Equal(t, ev.Message.Timestamp, ev.Message.ThreadTimestamp,
		"a top-level message is the root of its own thread; ThreadTimestamp must match "+
			"Timestamp or skipMessageEvent's unfurl check (see #266) treats every regular "+
			"message as an unfurl and drops it")
}

func TestSkipMessageEventDoesNotDropRegularMessages(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	cfg := &bridge.Config{Bridge: &bridge.Bridge{Log: logrus.NewEntry(logger)}}
	b := newBridge(cfg)
	b.si = &slack.Bot{ID: "BBOTID", UserID: "UBOTUSER"}

	var ev slackevents.MessageEvent
	require.NoError(t, json.Unmarshal([]byte(regularTopLevelMessageJSON), &ev))

	assert.False(t, b.skipMessageEvent(&ev),
		"a plain message from a real human user must not be skipped")
}

func TestSkipMessageEventThreadReplies(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	b := newBridge(&bridge.Config{Bridge: &bridge.Bridge{Log: logrus.NewEntry(logger)}})
	b.si = &slack.Bot{ID: "BBOTID", UserID: "UBOTUSER"}
	for _, tc := range []struct {
		name     string
		body     string
		wantSkip bool
	}{
		{"new reply", `{"type":"message","user":"human","text":"reply","ts":"2.0","thread_ts":"1.0"}`, false},
		{"broadcast reply", `{"type":"message","subtype":"thread_broadcast","user":"human","text":"reply","ts":"2.0","thread_ts":"1.0"}`, false},
		{"edited reply", `{"type":"message","subtype":"message_changed","message":{"user":"human","text":"edited","ts":"2.0","thread_ts":"1.0","edited":{"user":"human","ts":"3.0"}}}`, false},
		{"unfurl update", `{"type":"message","subtype":"message_changed","message":{"user":"human","text":"reply","ts":"2.0","thread_ts":"1.0"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ev slackevents.MessageEvent
			require.NoError(t, json.Unmarshal([]byte(tc.body), &ev))
			assert.Equal(t, tc.wantSkip, b.skipMessageEvent(&ev))
		})
	}
}

func TestPopulateThreadParent(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "slack.test"})
	br.Config = config.NewConfigFromString(logger, []byte("[slack.test]\n"))
	br.Log = logrus.NewEntry(logger)
	b := newBridge(&bridge.Config{Bridge: br})
	b.channels = newChannelManager(br.Log, nil)
	b.channels.channelsByID["channel"] = &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "channel"}, Name: "general"}}
	for _, tc := range []struct {
		name   string
		parent string
		want   string
	}{
		{"root message or edit", "1.0", ""},
		{"thread reply", "0.5", "0.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := &slackevents.MessageEvent{Channel: "channel", TimeStamp: "1.0", Message: &slack.Msg{Timestamp: "1.0", ThreadTimestamp: tc.parent}}
			msg, err := b.populateReceivedMessage(ev)
			require.NoError(t, err)
			assert.Equal(t, tc.want, msg.ParentID)
		})
	}
}

func TestDownloadVideo(t *testing.T) {
	for _, privateOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "download URL", true: "private URL fallback"}[privateOnly], func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				_, _ = io.WriteString(w, "video")
			}))
			defer server.Close()
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			br := bridge.New(&config.Bridge{Account: "slack.test"})
			br.Config = config.NewConfigFromString(logger, []byte("[slack.test]\nToken=\"test-token\"\n"))
			br.Log = logrus.NewEntry(logger)
			br.General = &config.Protocol{MediaDownloadSize: 20}
			b := newBridge(&bridge.Config{Bridge: br})
			file := slack.File{ID: "video", Name: "clip.mp4", Size: 5, URLPrivate: server.URL}
			if !privateOnly {
				file.URLPrivateDownload = server.URL
				file.URLPrivate = "http://unused.invalid"
			}
			msg := &config.Message{Text: "watch this", Extra: make(map[string][]interface{})}
			b.handleAttachments(&slackevents.MessageEvent{Channel: "channel", Message: &slack.Msg{Files: []slack.File{file}}}, msg)
			require.Len(t, msg.Extra["file"], 1)
			got := msg.Extra["file"][0].(config.FileInfo)
			assert.Equal(t, "clip.mp4", got.Name)
			assert.Equal(t, "video", got.NativeID)
			assert.Equal(t, "video", string(*got.Data))
			assert.Equal(t, "watch this", got.Comment)
			assert.Equal(t, server.URL, got.URL)
			assert.Empty(t, msg.Text)
			assert.Equal(t, 1, requests)
			assert.True(t, b.cache.Contains(cfileDownloadChannel+file.ID))
		})
	}
}

func TestDownloadVideoAboveOneMegabyte(t *testing.T) {
	data := strings.Repeat("v", 2534730)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, data)
	}))
	defer server.Close()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "slack.test"})
	br.Config = config.NewConfigFromString(logger, []byte("[general]\nMediaDownloadSize=10000000\n[slack.test]\nToken=\"test-token\"\n"))
	br.Log = logrus.NewEntry(logger)
	br.General = &br.Config.BridgeValues().General
	b := newBridge(&bridge.Config{Bridge: br})
	msg := &config.Message{Text: "caption", Extra: make(map[string][]interface{})}
	err := b.handleDownloadFile(msg, &slack.File{ID: "video", Name: "IMG_1701.MOV", Size: len(data), URLPrivate: server.URL}, false)
	require.NoError(t, err)
	require.Len(t, msg.Extra["file"], 1)
	file := msg.Extra["file"][0].(config.FileInfo)
	assert.Equal(t, data, string(*file.Data))
	assert.Equal(t, "caption", file.Comment)
}

func TestUploadMediaToSlackPreservesBytesAndThread(t *testing.T) {
	for _, filename := range []string{"photo.png", "clip.MOV"} {
		t.Run(filename, func(t *testing.T) {
			data := []byte("original media bytes")
			var endpoint string
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files.getUploadURLExternal":
					assert.Equal(t, filename, r.FormValue("filename"))
					assert.Equal(t, "20", r.FormValue("length"))
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "file_id": "file", "upload_url": endpoint + "/upload"})
				case "/upload":
					file, header, err := r.FormFile("file")
					require.NoError(t, err)
					defer file.Close()
					assert.Equal(t, filename, header.Filename)
					uploaded, err := io.ReadAll(file)
					require.NoError(t, err)
					assert.Equal(t, data, uploaded)
					_, _ = io.WriteString(w, `{}`)
				case "/files.completeUploadExternal":
					assert.Equal(t, "channel", r.FormValue("channel_id"))
					assert.Equal(t, "1.0", r.FormValue("thread_ts"))
					assert.Equal(t, "caption", r.FormValue("initial_comment"))
					assert.Equal(t, "Alice [Discord]", r.FormValue("username"))
					assert.Equal(t, "https://example.com/alice.png", r.FormValue("icon_url"))
					_, _ = io.WriteString(w, `{"ok":true,"files":[{"id":"file"}]}`)
				case "/files.info":
					_, _ = io.WriteString(w, `{"ok":true,"file":{"id":"file","shares":{"private":{"channel":[{"ts":"2.0"}]}}}}`)
				default:
					t.Errorf("unexpected Slack request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			endpoint = server.URL
			b := identityTestBridge(server.URL, false)
			id, err := b.uploadFile(&config.Message{Account: "discord.test", Username: "Alice [Discord]", Avatar: "https://example.com/alice.png", ParentID: "1.0", Extra: map[string][]interface{}{
				"file": {config.FileInfo{Name: filename, Data: &data, Comment: "caption"}},
			}}, "channel")
			require.NoError(t, err)
			assert.Equal(t, "2.0", id)
			assert.Equal(t, []string{"/files.getUploadURLExternal", "/upload", "/files.completeUploadExternal", "/files.info"}, calls)
		})
	}
}

func TestUploadFileWaitsForMessageID(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, apiError string
		files, missingShares       int
		rateLimited                bool
		wantError                  string
	}{
		{name: "delayed public share", visibility: "public", files: 1, missingShares: 2},
		{name: "delayed private share", visibility: "private", files: 1, missingShares: 1},
		{name: "multiple files keep first message as root", visibility: "public", files: 4},
		{name: "rate limit is retried", visibility: "public", files: 1, rateLimited: true},
		{name: "missing share is an error", files: 1, missingShares: 5, wantError: "no message timestamp"},
		{name: "API error is reported", files: 1, apiError: "missing_scope", wantError: "missing_scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var endpoint string
			uploads, completed, infoCalls := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files.getUploadURLExternal":
					uploads++
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "file_id": fmt.Sprintf("file%d", uploads), "upload_url": endpoint + "/upload"})
				case "/upload":
					_, _ = io.WriteString(w, `{}`)
				case "/files.completeUploadExternal":
					completed++
					_, _ = fmt.Fprintf(w, `{"ok":true,"files":[{"id":"file%d"}]}`, completed)
				case "/files.info":
					infoCalls++
					assert.Equal(t, tc.files, completed, "all files must be sent before resolving the root")
					assert.Equal(t, "file1", r.FormValue("file"))
					switch {
					case tc.rateLimited && infoCalls == 1:
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(http.StatusTooManyRequests)
					case tc.apiError != "":
						_, _ = fmt.Fprintf(w, `{"ok":false,"error":%q}`, tc.apiError)
					case infoCalls <= tc.missingShares:
						_, _ = io.WriteString(w, `{"ok":true,"file":{"id":"file1","shares":{}}}`)
					default:
						_, _ = fmt.Fprintf(w, `{"ok":true,"file":{"id":"file1","shares":{%q:{"channel":[{"ts":"2.0"}]}}}}`, tc.visibility)
					}
				default:
					t.Errorf("unexpected Slack request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			endpoint = server.URL
			b := identityTestBridge(endpoint, false)
			data := []byte("image bytes")
			files := make([]interface{}, tc.files)
			for i := range files {
				files[i] = config.FileInfo{Name: "image.png", Data: &data}
			}
			id, err := b.uploadFile(&config.Message{Account: "discord.test", Extra: map[string][]interface{}{"file": files}}, "channel")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Empty(t, id)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "2.0", id)
			}
			assert.Equal(t, tc.files, uploads, "metadata retries must not upload files again")
			assert.Equal(t, tc.files, completed)
			if tc.apiError != "" {
				assert.Equal(t, 1, infoCalls)
			} else if tc.rateLimited {
				assert.Equal(t, 2, infoCalls)
			} else {
				assert.Equal(t, min(tc.missingShares+1, 5), infoCalls)
			}
		})
	}
}

func TestMissingDiscordThreadParentDoesNotPostToSlackChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a thread reply with no parent must not call Slack: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	b := identityTestBridge(server.URL, false)
	b.Config.Bridge.Config = &config.TestConfig{Config: b.Config.Bridge.Config, Overrides: map[string]interface{}{"slack.test.PreserveThreading": true}}
	id, err := b.Send(config.Message{Channel: "ID:channel", Account: "discord.test", Protocol: "discord", ParentID: config.ParentIDNotFound, Text: "thread reply"})
	require.ErrorContains(t, err, "parent message mapping is missing")
	assert.Empty(t, id)
}
