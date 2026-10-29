package gateway

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/gateway/bridgemap"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func persistentTestRouter(t *testing.T, path string) *Router {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	cfg := config.NewConfigFromString(logger, append([]byte(fmt.Sprintf("[general]\nMessageStorePath=%q\n", path)), testconfig...))
	r, err := NewRouter(logger, cfg, bridgemap.FullMap)
	require.NoError(t, err)
	t.Cleanup(func() { r.messageStore.db.Close() })
	return r
}

func TestThreadMappingsSurviveRestartAndCacheEviction(t *testing.T) {
	for _, origin := range []string{"slack", "discord"} {
		t.Run(origin, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "links.sqlite3")
			r := persistentTestRouter(t, path)
			gw := r.Gateways["bridge1"]
			slack, discord := gw.Bridges["slack.test"], gw.Bridges["discord.test"]
			source := "slack 1.0"
			ids := []*BrMsgID{{discord, "discord root;second-part", "generaldiscord.test"}}
			if origin == "discord" {
				source = "discord root"
				ids = []*BrMsgID{{slack, "slack 1.0", "testingslack.test"}}
			}
			require.NoError(t, gw.rememberMessage(source, ids))
			gw.Messages.Purge()
			assert.Equal(t, source, gw.FindCanonicalMsgID("slack", "1.0"))
			assert.Equal(t, source, gw.FindCanonicalMsgID("discord", "root"))
			require.NoError(t, r.messageStore.db.Close())

			r = persistentTestRouter(t, path)
			gw = r.Gateways["bridge1"]
			for _, direction := range []struct{ from, parent, channel, to, want string }{
				{"slack", "1.0", "testing", "discord", "root"},
				{"discord", "root", "general", "slack", "1.0"},
			} {
				dest := gw.Bridges[direction.to+".test"]
				dest.Config = &config.TestConfig{Config: dest.Config, Overrides: map[string]interface{}{dest.Account + ".PreserveThreading": true}}
				capture := &threadDestination{}
				dest.Bridger = capture
				result := gw.handleMessage(&config.Message{
					ID: "reply", ParentID: direction.parent, Channel: direction.channel,
					Account: direction.from + ".test", Protocol: direction.from, Gateway: "bridge1", Text: "reply",
				}, dest)
				require.Len(t, result, 1)
				if direction.to == "discord" && origin == "slack" {
					assert.Equal(t, "root;second-part", capture.message.ParentID)
				} else {
					assert.Equal(t, direction.want, capture.message.ParentID)
				}
			}
			if origin == "slack" {
				assert.Equal(t, source, gw.FindCanonicalMsgID("discord", "second-part"))
			}
			assert.Empty(t, gw.FindCanonicalMsgID("slack", "unknown"))
			other, err := r.messageStore.canonical("other-gateway", "slack 1.0")
			require.NoError(t, err)
			assert.Empty(t, other)
			wrongChannel, err := r.messageStore.destination("bridge1", source, ids[0].br.Account, "other-channel")
			require.NoError(t, err)
			assert.Empty(t, wrongChannel)
		})
	}
}

func TestMessageStoreFailureDoesNotPopulateCache(t *testing.T) {
	r := persistentTestRouter(t, filepath.Join(t.TempDir(), "links.sqlite3"))
	gw := r.Gateways["bridge1"]
	_, err := r.messageStore.db.Exec(`CREATE TRIGGER reject_links BEFORE INSERT ON message_links
		BEGIN SELECT RAISE(ABORT, 'write failed'); END`)
	require.NoError(t, err)
	err = gw.rememberMessage("slack 1.0", []*BrMsgID{{gw.Bridges["discord.test"], "discord root", "generaldiscord.test"}})
	require.Error(t, err)
	assert.False(t, gw.Messages.Contains("slack 1.0"))
	id, err := r.messageStore.canonical("bridge1", "slack 1.0")
	require.NoError(t, err)
	assert.Empty(t, id, "the partial alias write must be rolled back")
}

func TestMessageStoreInvalidPathFailsStartup(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	path := filepath.Join(t.TempDir(), "missing", "links.sqlite3")
	cfg := config.NewConfigFromString(logger, []byte(fmt.Sprintf("[general]\nMessageStorePath=%q\n", path)))
	_, err := NewRouter(logger, cfg, bridgemap.FullMap)
	require.Error(t, err)
}
