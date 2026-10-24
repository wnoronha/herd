package agent

import "os"

// ApplyConfigDefaults fills zero-value Config fields from environment and sensible defaults.
func ApplyConfigDefaults(cfg *Config) {
	if cfg.OpenAIBaseURL == "" {
		if envBase := os.Getenv("OPENAI_BASE_URL"); envBase != "" {
			cfg.OpenAIBaseURL = envBase
		} else if envBase := os.Getenv("OPENAI_API_BASE"); envBase != "" {
			cfg.OpenAIBaseURL = envBase
		} else {
			cfg.OpenAIBaseURL = "http://localhost:11434/v1"
		}
	}
	if cfg.OpenAIAPIKey == "" {
		if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
			cfg.OpenAIAPIKey = envKey
		}
	}
	if cfg.OpenAIModel == "" {
		if envModel := os.Getenv("OPENAI_MODEL"); envModel != "" {
			cfg.OpenAIModel = envModel
		} else {
			cfg.OpenAIModel = "gpt-4o-mini"
		}
	}
	if cfg.APIKey == "" {
		if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" {
			cfg.APIKey = envKey
		} else if envKey := os.Getenv("GOOGLE_API_KEY"); envKey != "" {
			cfg.APIKey = envKey
		}
	}
	if cfg.Model == "" {
		if envModel := os.Getenv("GEMINI_MODEL"); envModel != "" {
			cfg.Model = envModel
		} else {
			cfg.Model = "gemini-3.8-flash"
		}
	}
	if cfg.OllamaHost == "" {
		cfg.OllamaHost = os.Getenv("OLLAMA_HOST")
		if cfg.OllamaHost == "" {
			cfg.OllamaHost = "http://localhost:11434"
		}
	}
}
