package helper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionEmojiMapFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reactions.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"brilliant":"bangbang","party":"party:1234"}`), 0600))
	mapping, err := LoadReactionEmojiMap(path)
	require.NoError(t, err)
	assert.Equal(t, "‼️", mapping.Discord("brilliant"))
	assert.Equal(t, "brilliant", mapping.Slack("‼"))
	assert.Equal(t, "brilliant", mapping.Slack("‼️"))
	assert.Equal(t, "party:1234", mapping.Discord("party"))
	assert.Equal(t, "party", mapping.Slack("party:1234"))
	assert.Empty(t, mapping.Slack("unknown:1234"))

	// Changing one entry also changes its inverse on the next load.
	require.NoError(t, os.WriteFile(path, []byte(`{"brilliant":"✨"}`), 0600))
	mapping, err = LoadReactionEmojiMap(path)
	require.NoError(t, err)
	assert.Equal(t, "✨", mapping.Discord("brilliant"))
	assert.Equal(t, "brilliant", mapping.Slack("✨"))
	assert.Equal(t, "bangbang", mapping.Slack("‼️"))
	assert.Empty(t, mapping.Slack("party:1234"))
}

func TestReactionEmojiMapValidation(t *testing.T) {
	for _, content := range []string{
		`broken`, `null`, `[]`, `{"brilliant":12}`, `{"":"‼️"}`,
		`{"brilliant":""}`, `{" brilliant":"‼️"}`, `{"brilliant":" ‼️"}`,
		`{":brilliant:":"‼️"}`, `{"one":"‼️","two":"‼"}`,
		`{"one":"bangbang","two":"‼️"}`, `{"one":"party:1234","two":"party:1234"}`,
	} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reactions.json")
			require.NoError(t, os.WriteFile(path, []byte(content), 0600))
			_, err := LoadReactionEmojiMap(path)
			require.Error(t, err)
		})
	}
	_, err := LoadReactionEmojiMap(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
	mapping, err := LoadReactionEmojiMap("")
	require.NoError(t, err)
	assert.Equal(t, "👍", mapping.Discord("+1"))
	assert.Equal(t, "+1", mapping.Slack("👍"))
	assert.Empty(t, mapping.Discord("brilliant"))
}
