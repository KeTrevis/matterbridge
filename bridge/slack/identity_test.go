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
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	br := bridge.New(&config.Bridge{Account: "slack.test"})
	br.Config = config.NewConfigFromString(logger, []byte("[slack.test]\nPrefixMessagesWithNick=false\n"))
	br.Log = logrus.NewEntry(logger)
	b := newBridge(&bridge.Config{Bridge: br})
	msg := config.Message{Account: "discord.test", Username: "Alice [Discord]", Avatar: "https://example.com/alice.png", Text: "hello", ParentID: "1.0"}
	client := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	_, _, err := client.PostMessage("channel", b.prepareMessageOptions(&msg)...)
	require.NoError(t, err)
}
