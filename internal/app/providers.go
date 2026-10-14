package app

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/config"
	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/routing"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
)

type fakeSettings struct {
	LatencyMS  int                             `json:"latency_ms"`
	FailModels map[string]providers.ErrorClass `json:"fail_models"`
}

func ProviderFactory(appConfig config.Config) routing.Factory {
	return func(providerConfig storage.ProviderConfig) (providers.Provider, error) {
		switch providerConfig.Kind {
		case "fake":
			var settings fakeSettings
			if len(providerConfig.Settings) > 0 {
				if err := json.Unmarshal(providerConfig.Settings, &settings); err != nil {
					return nil, fmt.Errorf("decode fake settings: %w", err)
				}
			}
			return providers.NewFake(providers.FakeConfig{ProviderName: providerConfig.Name, Latency: time.Duration(settings.LatencyMS) * time.Millisecond, FailModels: settings.FailModels}), nil
		case "openai-compatible":
			baseURL := providerConfig.BaseURL
			if baseURL == "" {
				baseURL = appConfig.OpenAIBaseURL
			}
			key := appConfig.OpenAIAPIKey
			if providerConfig.APIKeyEnv != "" {
				key = os.Getenv(providerConfig.APIKeyEnv)
			}
			if key == "" {
				return nil, fmt.Errorf("API key is not configured")
			}
			return providers.NewOpenAICompatible(providers.OpenAICompatibleConfig{ProviderName: providerConfig.Name, BaseURL: baseURL, APIKey: key}), nil
		case "gemini":
			key := appConfig.GeminiAPIKey
			if providerConfig.APIKeyEnv != "" {
				key = os.Getenv(providerConfig.APIKeyEnv)
			}
			if key == "" {
				return nil, fmt.Errorf("API key is not configured")
			}
			return providers.NewGemini(providers.GeminiConfig{ProviderName: providerConfig.Name, BaseURL: providerConfig.BaseURL, APIKey: key}), nil
		default:
			return nil, fmt.Errorf("unsupported provider kind %q", providerConfig.Kind)
		}
	}
}
