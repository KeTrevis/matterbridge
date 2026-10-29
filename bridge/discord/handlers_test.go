package bdiscord

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/stretchr/testify/require"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestHandleEmbed(t *testing.T) {
	testcases := map[string]struct {
		embed  *discordgo.MessageEmbed
		result string
	}{
		"allempty": {
			embed:  &discordgo.MessageEmbed{},
			result: "",
		},
		"one": {
			embed: &discordgo.MessageEmbed{
				Title: "blah",
			},
			result: " embed: blah\n",
		},
		"two": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
			},
			result: " embed: blah - blah2\n",
		},
		"three": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
				URL:         "blah3",
			},
			result: " embed: blah - blah2 - blah3\n",
		},
		"twob": {
			embed: &discordgo.MessageEmbed{
				Description: "blah2",
				URL:         "blah3",
			},
			result: " embed: blah2 - blah3\n",
		},
		"oneb": {
			embed: &discordgo.MessageEmbed{
				URL: "blah3",
			},
			result: " embed: blah3\n",
		},
	}

	for name, tc := range testcases {
		assert.Equalf(t, tc.result, handleEmbed(tc.embed), "Testcases %s", name)
	}
}

func TestForwardVideoAttachments(t *testing.T) {
	for _, tc := range []struct {
		name, filename, contentType, caption, body string
		status, declaredSize, limit                int
		blacklist                                  []string
		wantFile, wantURL                          bool
	}{
		{name: "video only", filename: "clip.mp4", contentType: "video/mp4", body: "video", status: 200, declaredSize: 5, limit: 20, wantFile: true},
		{name: "caption and missing MIME type", filename: "clip.WEBM", caption: "watch this", body: "video", status: 200, declaredSize: 5, limit: 20, wantFile: true},
		{name: "download failure keeps link", filename: "clip.mp4", status: 503, limit: 20, wantURL: true},
		{name: "actual size exceeds limit", filename: "clip.mp4", body: "too much data", status: 200, declaredSize: 1, limit: 5, wantURL: true},
		{name: "declared size exceeds limit", filename: "clip.mp4", declaredSize: 21, limit: 20},
		{name: "blacklisted", filename: "clip.mp4", blacklist: []string{`\.mp4$`}, limit: 20},
		{name: "other files stay links", filename: "photo.png", limit: 20, wantURL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			b := threadBridge(t, false)
			b.General.MediaDownloadSize = tc.limit
			b.General.MediaDownloadBlackList = tc.blacklist
			b.messageCreate(b.c, &discordgo.MessageCreate{Message: &discordgo.Message{
				ID: "message", GuildID: "guild", ChannelID: "main", Content: tc.caption,
				Author:           &discordgo.User{ID: "user", Username: "Alice"},
				MessageReference: &discordgo.MessageReference{MessageID: "parent", ChannelID: "main"},
				Attachments:      []*discordgo.MessageAttachment{{ID: "video", Filename: tc.filename, ContentType: tc.contentType, URL: server.URL, Size: tc.declaredSize}},
			}})
			if tc.name == "blacklisted" {
				assert.Empty(t, b.Remote)
				assert.Zero(t, requests)
				return
			}
			require.Len(t, b.Remote, 1)
			msg := <-b.Remote
			assert.Equal(t, "parent", msg.ParentID)
			assert.Equal(t, "Alice", msg.Username)
			if tc.wantFile {
				require.Len(t, msg.Extra["file"], 1)
				file := msg.Extra["file"][0].(config.FileInfo)
				assert.Equal(t, tc.filename, file.Name)
				assert.Equal(t, "video", file.NativeID)
				assert.Equal(t, tc.body, string(*file.Data))
				assert.Equal(t, tc.caption, file.Comment)
				assert.Empty(t, msg.Text)
			} else {
				assert.Empty(t, msg.Extra["file"])
			}
			if tc.wantURL {
				assert.Contains(t, msg.Text, server.URL)
			}
			if tc.name == "declared size exceeds limit" {
				assert.Equal(t, config.EventFileFailureSize, msg.Event)
				assert.Zero(t, requests)
			}
		})
	}
}

func TestVideoUploadPreservesBytesAndCaption(t *testing.T) {
	b := threadBridge(t, false, discordRequest{"POST", "/channels/main/messages", 200, `{"id":"upload"}`, func(t *testing.T, r *http.Request) {
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		payload, err := reader.NextPart()
		require.NoError(t, err)
		body, err := io.ReadAll(payload)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"content":"Alice: watch this"`)
		file, err := reader.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "clip.mp4", file.FileName())
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		assert.Equal(t, "video", string(data))
	}})
	data := []byte("video")
	id, err := b.handleUploadFile(&config.Message{Username: "Alice: ", Extra: map[string][]interface{}{
		"file": {config.FileInfo{Name: "clip.mp4", NativeID: "original", Comment: "watch this", Data: &data}},
	}}, "main")
	require.NoError(t, err)
	assert.Equal(t, "upload", id)
}

func TestMixedVideoAttachmentsPreserveLinksAndCaption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "video")
	}))
	defer server.Close()
	b := threadBridge(t, false)
	b.General.MediaDownloadSize = 20
	msg := &config.Message{Text: "watch this"}
	b.handleAttachments([]*discordgo.MessageAttachment{
		{ID: "first", Filename: "first.mp4", URL: server.URL, Size: 5},
		{ID: "second", Filename: "second.mov", URL: server.URL, Size: 5},
		{Filename: "photo.png", URL: "https://example.com/photo.png"},
	}, msg)
	require.Len(t, msg.Extra["file"], 2)
	assert.Equal(t, "watch this\nhttps://example.com/photo.png", msg.Extra["file"][0].(config.FileInfo).Comment)
	assert.Empty(t, msg.Extra["file"][1].(config.FileInfo).Comment)
	assert.Empty(t, msg.Text)
}
