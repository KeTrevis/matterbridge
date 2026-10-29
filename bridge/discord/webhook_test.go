package bdiscord

import (
	"encoding/json"
	"net/http"
	"testing"

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
