package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	GatewayAPIKey        string
	UpstreamTokens       []string
	UpstreamBaseURL      *url.URL
	Port                 string
	TokenCooldownSeconds int
}

func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}

	gatewayAPIKey := strings.TrimSpace(os.Getenv("GATEWAY_API_KEY"))
	if gatewayAPIKey == "" {
		return Config{}, fmt.Errorf("missing GATEWAY_API_KEY")
	}

	upstreamTokens := splitAndTrim(os.Getenv("UPSTREAM_TOKENS"))
	if len(upstreamTokens) == 0 {
		return Config{}, fmt.Errorf("missing UPSTREAM_TOKENS")
	}

	baseURLValue := getenvDefault("UPSTREAM_BASE_URL", "https://ai.patungin.id/v1")
	baseURL, err := url.Parse(baseURLValue)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return Config{}, fmt.Errorf("invalid UPSTREAM_BASE_URL")
	}

	cooldownSeconds, err := parseIntDefault("TOKEN_COOLDOWN_SECONDS", 60)
	if err != nil {
		return Config{}, fmt.Errorf("invalid TOKEN_COOLDOWN_SECONDS: %w", err)
	}

	return Config{
		GatewayAPIKey:        gatewayAPIKey,
		UpstreamTokens:       upstreamTokens,
		UpstreamBaseURL:      baseURL,
		Port:                 getenvDefault("PORT", "8080"),
		TokenCooldownSeconds: cooldownSeconds,
	}, nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key == "" {
			continue
		}

		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}

	return nil
}

func splitAndTrim(value string) []string {
	parts := strings.Split(value, ",")
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		tokens = append(tokens, trimmed)
	}
	return tokens
}

func getenvDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parseIntDefault(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}
