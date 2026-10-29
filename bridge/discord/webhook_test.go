package bdiscord

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/discord/transmitter"
	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enableTestWebhooks(b *Bdiscord) {
	b.useAutoWebhooks = true
	b.transmitter = transmitter.New(b.c, b.guildID, "bridge", false)
	b.transmitter.AddWebhook("main", &discordgo.Webhook{ID: "hook", Token: "token", ChannelID: "main"})
}

func checkWebhookIdentity(threadID string) func(*testing.T, *http.Request) {
	return func(t *testing.T, r *http.Request) {
		assert.Equal(t, threadID, r.URL.Query().Get("thread_id"))
		var body discordgo.WebhookParams
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Alice [Slack]", body.Username)
		assert.Equal(t, "https://example.com/alice.png", body.AvatarURL)
		assert.Equal(t, "hello", body.Content)
		require.NotNil(t, body.AllowedMentions)
		assert.Empty(t, body.AllowedMentions.Parse)
	}
}

func TestWebhookSenderIdentity(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"root"}`, checkWebhookIdentity("")},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"reply"}`, checkWebhookIdentity("root")},
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"PATCH", "/webhooks/hook/token/messages/reply", 200, `{"id":"reply"}`, func(t *testing.T, r *http.Request) {
			assert.Equal(t, "root", r.URL.Query().Get("thread_id"))
		}},
		discordRequest{"DELETE", "/channels/root/messages/reply", 204, "", nil},
	)
	enableTestWebhooks(b)
	msg := config.Message{Channel: "ID:main", Username: "Alice [Slack]", Avatar: "https://example.com/alice.png", Text: "hello"}
	id, err := b.Send(msg)
	require.NoError(t, err)
	assert.Equal(t, "root", id)
	msg.ParentID = id
	id, err = b.Send(msg)
	require.NoError(t, err)
	assert.Equal(t, "reply", id)
	msg.ID, msg.ParentID, msg.Text = id, "", "edited"
	_, err = b.Send(msg)
	require.NoError(t, err)
	msg.Event = config.EventMsgDelete
	_, err = b.Send(msg)
	require.NoError(t, err)
}

func TestWebhookThreadFileAndDeletion(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"file-message"}`, func(t *testing.T, r *http.Request) {
			assert.Equal(t, "root", r.URL.Query().Get("thread_id"))
			require.NoError(t, r.ParseMultipartForm(1024))
			var body discordgo.WebhookParams
			require.NoError(t, json.Unmarshal([]byte(r.FormValue("payload_json")), &body))
			assert.Equal(t, "Alice [Slack]", body.Username)
			assert.Equal(t, "https://example.com/alice.png", body.AvatarURL)
		}},
		discordRequest{"DELETE", "/channels/root/messages/file-message", 204, "", nil},
	)
	enableTestWebhooks(b)
	data := []byte("attachment")
	id, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Username: "Alice [Slack]", Avatar: "https://example.com/alice.png", Extra: map[string][]interface{}{
		"file": {config.FileInfo{Name: "image.png", Data: &data, NativeID: "slack-file"}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "file-message", id)
	_, err = b.Send(config.Message{Channel: "ID:main", ID: "slack-file", Event: config.EventFileDelete})
	require.NoError(t, err)
}

func TestWebhookRootEditStaysInMainChannel(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"PATCH", "/webhooks/hook/token/messages/root", 200, `{"id":"root"}`, func(t *testing.T, r *http.Request) {
			assert.Empty(t, r.URL.Query().Get("thread_id"))
		}},
	)
	enableTestWebhooks(b)
	id, err := b.Send(config.Message{Channel: "ID:main", ID: "root", ParentID: "root", Text: "edited root", Username: "Alice [Slack]"})
	require.NoError(t, err)
	assert.Equal(t, "root", id)
}

func TestWebhookDisplayNameUnicode(t *testing.T) {
	for _, name := range []string{"Élodie 🙂 [Slack]", strings.Repeat("é", 79) + "🙂", strings.Repeat("é", 100)} {
		t.Run(name, func(t *testing.T) {
			b := threadBridge(t, true, discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"message"}`, func(t *testing.T, r *http.Request) {
				var body discordgo.WebhookParams
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				want := []rune(name)
				if len(want) > 80 {
					want = want[:80]
				}
				assert.Equal(t, string(want), body.Username)
				assert.True(t, utf8.ValidString(body.Username))
			}})
			enableTestWebhooks(b)
			_, err := b.Send(config.Message{Channel: "ID:main", Username: name, Text: "hello"})
			require.NoError(t, err)
		})
	}
}

func TestWebhookSizeWarningPreservesSenderAndThread(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"GET", "/channels/root", 200, publicThreadJSON, nil},
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"warning"}`, func(t *testing.T, r *http.Request) {
			assert.Equal(t, "root", r.URL.Query().Get("thread_id"))
			var body discordgo.WebhookParams
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "Alice [Slack]", body.Username)
			assert.Equal(t, "https://example.com/alice.png", body.AvatarURL)
			assert.Contains(t, body.Content, "large.png")
			assert.Contains(t, body.Content, "caption")
		}},
	)
	enableTestWebhooks(b)
	_, err := b.Send(config.Message{Channel: "ID:main", ParentID: "root", Text: "caption", Username: "Alice [Slack]", Avatar: "https://example.com/alice.png", Event: config.EventFileFailureSize, Extra: map[string][]interface{}{
		config.EventFileFailureSize: {config.FileInfo{Name: "large.png", Size: 99}},
	}})
	require.NoError(t, err)
}

func TestWebhookMixedFilesPreserveSender(t *testing.T) {
	b := threadBridge(t, true,
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"text"}`, checkWebhookIdentity("")},
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"image"}`, checkWebhookFileIdentity("first caption")},
		discordRequest{"POST", "/webhooks/hook/token", 200, `{"id":"video"}`, checkWebhookFileIdentity("")},
	)
	enableTestWebhooks(b)
	data := []byte("file bytes")
	id, err := b.Send(config.Message{Channel: "ID:main", Text: "hello", Username: "Alice [Slack]", Avatar: "https://example.com/alice.png", Extra: map[string][]interface{}{
		"file": {config.FileInfo{Name: "photo.png", Data: &data, Comment: "first caption"}, config.FileInfo{Name: "video.mp4", Data: &data}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "text;image;video", id)
}

func checkWebhookFileIdentity(caption string) func(*testing.T, *http.Request) {
	return func(t *testing.T, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1024))
		var body discordgo.WebhookParams
		require.NoError(t, json.Unmarshal([]byte(r.FormValue("payload_json")), &body))
		assert.Equal(t, "Alice [Slack]", body.Username)
		assert.Equal(t, "https://example.com/alice.png", body.AvatarURL)
		assert.Equal(t, caption, body.Content)
		require.NotNil(t, body.AllowedMentions)
		assert.Empty(t, body.AllowedMentions.Parse)
	}
}

func TestDiscordSourceIdentity(t *testing.T) {
	for _, tc := range []struct{ name, avatar, memberAvatar, wantAvatar string }{
		{name: "default avatar", wantAvatar: "/embed/avatars/0.png"},
		{name: "static avatar", avatar: "photo", wantAvatar: "/avatars/user/photo.png"},
		{name: "animated avatar", avatar: "a_photo", wantAvatar: "/avatars/user/a_photo.gif"},
		{name: "guild avatar", avatar: "photo", memberAvatar: "guildphoto", wantAvatar: "/guilds/guild/users/user/avatars/guildphoto.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := threadBridge(t, true)
			author := &discordgo.User{ID: "user", Username: "Alice", Avatar: tc.avatar, Discriminator: "0"}
			b.messageCreate(b.c, &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "main", Content: "hello", Author: author, Member: &discordgo.Member{Avatar: tc.memberAvatar}}})
			require.Len(t, b.Remote, 1)
			got := <-b.Remote
			assert.Equal(t, "Alice", got.Username)
			assert.True(t, strings.HasSuffix(got.Avatar, tc.wantAvatar), got.Avatar)
		})
	}
	t.Run("current server nickname", func(t *testing.T) {
		b := threadBridge(t, true)
		b.Config.Config.(*config.TestConfig).Overrides["discord.test.UseUserName"] = false
		b.messageCreate(b.c, &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "main", Content: "hello", Author: &discordgo.User{ID: "user", Username: "alice"}, Member: &discordgo.Member{Nick: "Alice du serveur"}}})
		require.Len(t, b.Remote, 1)
		assert.Equal(t, "Alice du serveur", (<-b.Remote).Username)
	})
}
