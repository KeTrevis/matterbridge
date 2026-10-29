package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kyokomi/emoji/v2"
)

// ReactionEmojiMap maps Slack names to Discord emoji and is used in reverse for incoming Discord reactions.
type ReactionEmojiMap map[string]string

func LoadReactionEmojiMap(path string) (ReactionEmojiMap, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read reaction emoji mapping %q: %w", path, err)
	}
	var mapping ReactionEmojiMap
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, fmt.Errorf("decode reaction emoji mapping %q: %w", path, err)
	}
	if mapping == nil {
		return nil, fmt.Errorf("reaction emoji mapping %q must be a JSON object", path)
	}
	reverse := make(map[string]string, len(mapping))
	for slack, discord := range mapping {
		if slack == "" || discord == "" || strings.TrimSpace(slack) != slack || strings.TrimSpace(discord) != discord || strings.HasPrefix(slack, ":") || strings.HasSuffix(slack, ":") {
			return nil, fmt.Errorf("invalid reaction mapping %q in %q: use a Slack name without surrounding colons and a nonempty Discord emoji", slack, path)
		}
		key := normalizeDiscordEmoji(discord)
		if previous, exists := reverse[key]; exists {
			return nil, fmt.Errorf("ambiguous reaction mapping: %q and %q both map to %q", previous, slack, discord)
		}
		reverse[key] = slack
	}
	return mapping, nil
}

func discordEmoji(value string) string {
	if unicode, ok := emoji.CodeMap()[":"+strings.Trim(value, ":")+":"]; ok {
		return unicode
	}
	return value
}

func normalizeDiscordEmoji(value string) string {
	return strings.ReplaceAll(discordEmoji(value), "\ufe0f", "")
}

func (m ReactionEmojiMap) Discord(name string) string {
	if mapped := discordEmoji(m[name]); mapped != "" {
		return mapped
	}
	var result string
	for _, part := range strings.Split(name, "::") {
		value, ok := emoji.CodeMap()[":"+strings.Replace(part, "flag-", "flag_", 1)+":"]
		if !ok {
			return ""
		}
		result += value
	}
	return result
}

func (m ReactionEmojiMap) Slack(value string) string {
	for slack, discord := range m {
		if normalizeDiscordEmoji(discord) == normalizeDiscordEmoji(value) {
			return slack
		}
	}
	runes := []rune(value)
	if len(runes) == 2 && runes[0] >= 0x1f1e6 && runes[0] <= 0x1f1ff && runes[1] >= 0x1f1e6 && runes[1] <= 0x1f1ff {
		return "flag-" + string([]rune{'a' + runes[0] - 0x1f1e6, 'a' + runes[1] - 0x1f1e6})
	}
	if names := emoji.RevCodeMap()[value+"\ufe0f"]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	if names := emoji.RevCodeMap()[value]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	if names := emoji.RevCodeMap()[strings.ReplaceAll(value, "\ufe0f", "")]; len(names) > 0 {
		return strings.Trim(names[0], ":")
	}
	// Slack represents skin tones as a separate shortcode suffix.
	if len(runes) > 1 && runes[len(runes)-1] >= 0x1f3fb && runes[len(runes)-1] <= 0x1f3ff {
		base := (ReactionEmojiMap{}).Slack(string(runes[:len(runes)-1]))
		tone := (ReactionEmojiMap{}).Slack(string(runes[len(runes)-1]))
		if base != "" && tone != "" {
			return base + "::" + tone
		}
	}
	return ""
}
