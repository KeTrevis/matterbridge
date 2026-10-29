package bdiscord

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type discordTransport func(*http.Request) (*http.Response, error)

func (f discordTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type discordRequest struct {
	method string
	path   string
	status int
	body   string
	check  func(*testing.T, *http.Request)
}

func threadBridge(t *testing.T, native bool, requests ...discordRequest) *Bdiscord {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "discord.test"})
	br.Config = config.NewConfigFromString(logger, []byte("[discord.test]\nPreserveThreading=true\nUseUserName=true\nAllowMention=[]\n"))
	br.Config = &config.TestConfig{Config: br.Config, Overrides: map[string]interface{}{"discord.test.UseNativeThreads": native}}
	br.Log = logrus.NewEntry(logger)
	br.General = &config.Protocol{}
	b := New(&bridge.Config{Bridge: br, Remote: make(chan config.Message, 10)}).(*Bdiscord)
	var err error
	b.c, err = discordgo.New("Bot test")
	require.NoError(t, err)
	b.guildID = "guild"
	b.userID = "bot"
	b.nick = "bridge"
	require.NoError(t, b.c.State.GuildAdd(&discordgo.Guild{ID: b.guildID}))
	require.NoError(t, b.JoinChannel(config.ChannelInfo{ID: "main", Name: "ID:main"}))
	next := 0
	b.c.Client = &http.Client{Transport: discordTransport(func(r *http.Request) (*http.Response, error) {
		require.Less(t, next, len(requests), "unexpected Discord API call: %s %s", r.Method, r.URL.Path)
		want := requests[next]
		next++
		assert.Equal(t, want.method, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, want.path), "unexpected path: %s", r.URL.Path)
		if want.check != nil {
			want.check(t, r)
		}
		return &http.Response{StatusCode: want.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(want.body)), Request: r}, nil
	})}
	t.Cleanup(func() { assert.Equal(t, len(requests), next, "not all Discord requests were made") })
	return b
}

const publicThreadJSON = `{"id":"root","parent_id":"main","guild_id":"guild","type":11,"thread_metadata":{"archived":false,"locked":false}}`

func checkThreadMessage(t *testing.T, r *http.Request) {
	t.Helper()
	var body discordgo.MessageSend
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	assert.Equal(t, "Alice: reply", body.Content)
	assert.Nil(t, body.Reference, "the root message is outside the thread, so it cannot be a reply reference")
	require.NotNil(t, body.AllowedMentions)
	assert.Empty(t, body.AllowedMentions.Parse)
}

func TestNativeThreadCreateReuseEditDelete(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 404, `{"code":10003,"message":"Unknown Channel"}`, nil},
		discordRequest{"GET", "/channels/main/messages/root", 200, `{"id":"root","content":"A discussion"}`, nil},
		discordRequest{"POST", "/channels/main/messages/root/threads", 200, publicThreadJSON, func(t *testing.T, r *http.Request) {
			var body discordgo.ThreadStart
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "A discussion", body.Name)
			assert.Equal(t, 1440, body.AutoArchiveDuration)
		}},
		discordRequest{"POST", "/channels/root/messages", 200, `{"id":"reply1"}`, checkThreadMessage},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/channels/root/messages", 200, `{"id":"reply2"}`, checkThreadMessage},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"PATCH", "/channels/root/messages/reply1", 200, `{"id":"reply1"}`, nil},
		discordRequest{"DELETE", "/channels/root/messages/reply1", 204, "", nil},
	)
	msg := config.Message{Channel: "ID:main", ParentID: "root", Text: "reply", Username: "Alice: "}
	id, err := b.Send(msg)
	require.NoError(t, err)
	assert.Equal(t, "reply1", id)
	id, err = b.Send(msg)
	require.NoError(t, err)
	assert.Equal(t, "reply2", id)
	msg.ID, msg.ParentID, msg.Text = "reply1", "", "edited"
	_, err = b.Send(msg)
	require.NoError(t, err)
	msg.Event = config.EventMsgDelete
	_, err = b.Send(msg)
	require.NoError(t, err)
}

func TestNativeThreadErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"permission denied", 403, `{"code":50013,"message":"Missing Permissions"}`},
		{"wrong parent", 200, `{"id":"root","parent_id":"other","guild_id":"guild","type":11}`},
		{"private thread", 200, `{"id":"root","parent_id":"main","guild_id":"guild","type":12}`},
		{"locked thread", 200, `{"id":"root","parent_id":"main","guild_id":"guild","type":11,"thread_metadata":{"locked":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := threadBridge(t, true, discordRequest{"GET", "/channels/root", tc.status, tc.body, nil})
			_, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Text: "reply"})
			require.Error(t, err)
		})
	}
	t.Run("missing parent never flattens a reply into the main channel", func(t *testing.T) {
		b := threadBridge(t, true)
		_, err := b.Send(config.Message{Channel: "ID:main", ParentID: config.ParentIDNotFound, Text: "reply"})
		require.ErrorContains(t, err, "parent message")
	})
}

func TestNativeThreadUnarchive(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 200, `{"id":"root","parent_id":"main","guild_id":"guild","type":11,"thread_metadata":{"archived":true}}`, nil},
		discordRequest{"PATCH", "/channels/root", 200, publicThreadJSON, func(t *testing.T, r *http.Request) {
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]interface{}{"archived": false}, body)
		}},
		discordRequest{"POST", "/channels/root/messages", 200, `{"id":"reply"}`, nil},
	)
	_, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Text: "reply"})
	require.NoError(t, err)
}

func TestNativeThreadConcurrentCreation(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 404, `{"code":10003}`, nil},
		discordRequest{"GET", "/channels/main/messages/root", 200, `{"id":"root","content":"topic"}`, nil},
		discordRequest{"POST", "/channels/main/messages/root/threads", 400, `{"code":160004}`, nil},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/channels/root/messages", 200, `{"id":"reply"}`, nil},
	)
	_, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Text: "reply"})
	require.NoError(t, err)
}

func TestNativeThreadFilesAndDeletion(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/channels/root/messages", 200, `{"id":"file-message"}`, nil},
		discordRequest{"DELETE", "/channels/root/messages/file-message", 204, "", nil},
	)
	data := []byte("attachment")
	id, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Extra: map[string][]interface{}{
		"file": {config.FileInfo{Name: "image.png", Data: &data, NativeID: "slack-file"}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "file-message", id)
	_, err = b.Send(config.Message{Channel: "ID:main", ID: "slack-file", Event: config.EventFileDelete})
	require.NoError(t, err)
}

func TestNativeThreadRootEditAndLegacyReplies(t *testing.T) {
	t.Run("editing the root keeps it in the main channel", func(t *testing.T) {
		b := threadBridge(t, true, discordRequest{"PATCH", "/channels/main/messages/root", 200, `{"id":"root"}`, nil})
		_, err := b.Send(config.Message{Channel: "ID:main", ID: "root", ParentID: "root", Text: "edited root"})
		require.NoError(t, err)
	})
	t.Run("disabled keeps ordinary reply references", func(t *testing.T) {
		b := threadBridge(t, false, discordRequest{"POST", "/channels/main/messages", 200, `{"id":"reply"}`, func(t *testing.T, r *http.Request) {
			var body discordgo.MessageSend
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotNil(t, body.Reference)
			assert.Equal(t, "root", body.Reference.MessageID)
		}})
		_, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Text: "reply"})
		require.NoError(t, err)
	})
}

func TestDiscordThreadIncomingLifecycle(t *testing.T) {
	b := threadBridge(t, true)
	require.NoError(t, b.c.State.ChannelAdd(&discordgo.Channel{ID: "root", ParentID: "main", GuildID: "guild", Type: discordgo.ChannelTypeGuildPublicThread}))
	message := &discordgo.Message{ID: "reply", ChannelID: "root", GuildID: "guild", Content: "hello", Author: &discordgo.User{ID: "human", Username: "Alice"}, MessageReference: &discordgo.MessageReference{ChannelID: "root", MessageID: "another-reply"}}
	b.messageCreate(b.c, &discordgo.MessageCreate{Message: message})
	got := <-b.Remote
	assert.Equal(t, "ID:main", got.Channel)
	assert.Equal(t, "root", got.ParentID, "nested Discord replies belong to the Slack root thread")
	assert.Equal(t, "reply", got.ID)
	now := time.Now()
	message.EditedTimestamp = &now
	b.messageUpdate(b.c, &discordgo.MessageUpdate{Message: message})
	assert.Equal(t, "root", (<-b.Remote).ParentID)
	b.messageDelete(b.c, &discordgo.MessageDelete{Message: message})
	got = <-b.Remote
	assert.Equal(t, "ID:main", got.Channel)
	assert.Equal(t, config.EventMsgDelete, got.Event)
	b.messageDeleteBulk(b.c, &discordgo.MessageDeleteBulk{GuildID: "guild", ChannelID: "root", Messages: []string{"reply"}})
	assert.Equal(t, "ID:main", (<-b.Remote).Channel)
}

func TestDiscordThreadIncomingSafety(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   discordgo.ChannelType
		guild string
	}{
		{"private", discordgo.ChannelTypeGuildPrivateThread, "guild"},
		{"different guild", discordgo.ChannelTypeGuildPublicThread, "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := threadBridge(t, true)
			require.NoError(t, b.c.State.GuildAdd(&discordgo.Guild{ID: tc.guild}))
			require.NoError(t, b.c.State.ChannelAdd(&discordgo.Channel{ID: "root", ParentID: "main", GuildID: tc.guild, Type: tc.typ}))
			assert.False(t, b.routeIncomingThread(&config.Message{}, "root"))
		})
	}
	t.Run("thread absent from gateway state is resolved through REST", func(t *testing.T) {
		b := threadBridge(t, true, discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil})
		var msg config.Message
		require.True(t, b.routeIncomingThread(&msg, "root"))
		assert.Equal(t, "ID:main", msg.Channel)
		assert.Equal(t, "root", msg.ParentID)
	})
	t.Run("system thread messages are not relayed", func(t *testing.T) {
		b := threadBridge(t, true)
		for _, typ := range []discordgo.MessageType{discordgo.MessageTypeThreadCreated, discordgo.MessageTypeThreadStarterMessage} {
			b.messageCreate(b.c, &discordgo.MessageCreate{Message: &discordgo.Message{GuildID: "guild", Type: typ}})
		}
		assert.Empty(t, b.Remote)
	})
}
