package modelregistry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lobo235/cc-proxy/internal/config"
)

var contextWindowSuffix = regexp.MustCompile(`\[[0-9]+[kKmM]\]$`)

var CodexModels = []string{
	"gpt-5.2",
	"gpt-5.3-codex",
	"gpt-5.3-codex-spark",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.5",
}

var KimiModels = []string{
	"kimi-for-coding",
	"kimi-k2.6",
	"k2.6",
}

var AnthropicAliases = []string{
	"haiku",
	"claude-haiku-4-5",
	"claude-haiku-4-5-20251001",
	"sonnet",
	"claude-sonnet-4-6",
	"opus",
	"claude-opus-4-7",
}

type Provider string

const (
	ProviderCodex Provider = "codex"
	ProviderKimi  Provider = "kimi"
)

type Resolved struct {
	Provider    Provider
	Model       string
	ServiceTier string
}

func NormalizeIncomingModel(model string) string {
	return contextWindowSuffix.ReplaceAllString(model, "")
}

func Resolve(model string, aliasProvider config.AliasProvider) (Resolved, bool) {
	model = NormalizeIncomingModel(model)
	if contains(AnthropicAliases, model) {
		if aliasProvider == config.AliasProviderKimi {
			return Resolved{Provider: ProviderKimi, Model: "kimi-for-coding"}, true
		}
		return Resolved{Provider: ProviderCodex, Model: codexAliasModel(model)}, true
	}
	if strings.HasSuffix(model, "-fast") {
		base := strings.TrimSuffix(model, "-fast")
		if contains(CodexModels, base) {
			return Resolved{Provider: ProviderCodex, Model: base, ServiceTier: "priority"}, true
		}
	}
	if contains(CodexModels, model) {
		return Resolved{Provider: ProviderCodex, Model: model}, true
	}
	if contains(KimiModels, model) {
		return Resolved{Provider: ProviderKimi, Model: "kimi-for-coding"}, true
	}
	return Resolved{}, false
}

func SupportedMessage(aliasProvider config.AliasProvider) string {
	groups := map[string][]string{
		string(ProviderCodex): append([]string{}, CodexModels...),
		string(ProviderKimi):  append([]string{}, KimiModels...),
	}
	for _, m := range CodexModels {
		groups[string(ProviderCodex)] = append(groups[string(ProviderCodex)], m+"-fast")
	}
	if aliasProvider == config.AliasProviderKimi {
		groups[string(ProviderKimi)] = append(groups[string(ProviderKimi)], AnthropicAliases...)
	} else {
		groups[string(ProviderCodex)] = append(groups[string(ProviderCodex)], AnthropicAliases...)
	}
	for _, models := range groups {
		sort.Strings(models)
	}
	return fmt.Sprintf("Supported: codex: %s; kimi: %s.",
		strings.Join(groups[string(ProviderCodex)], ", "),
		strings.Join(groups[string(ProviderKimi)], ", "),
	)
}

func codexAliasModel(model string) string {
	switch model {
	case "haiku", "claude-haiku-4-5", "claude-haiku-4-5-20251001":
		return "gpt-5.4-mini"
	case "sonnet", "claude-sonnet-4-6":
		return "gpt-5.4"
	case "opus", "claude-opus-4-7":
		return "gpt-5.5"
	default:
		return model
	}
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
