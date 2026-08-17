package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type Command string

const (
	CommandAdd        Command = "add"
	CommandSetEdge    Command = "set-edge"
	CommandSetBackend Command = "set-backend"
	CommandStatus     Command = "status"
)

type Config struct {
	CFAPIToken       string
	CFZone           string
	DNSPodSecretID   string
	DNSPodSecretKey  string
	DNSPodRecordLine string
}

var names = []string{
	"CF_API_TOKEN", "CF_ZONE", "DNSPOD_SECRET_ID", "DNSPOD_SECRET_KEY", "DNSPOD_RECORD_LINE",
}

func Load(path string, command Command) (Config, error) {
	values := map[string]string{}
	fromFile, err := godotenv.Read(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read dotenv: %w", err)
	}
	for key, value := range fromFile {
		values[key] = value
	}
	for _, key := range names {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	return FromValues(values, command)
}

func FromValues(values map[string]string, command Command) (Config, error) {
	required := []string{"CF_API_TOKEN", "DNSPOD_SECRET_ID", "DNSPOD_SECRET_KEY"}
	var missing []string
	for _, name := range required {
		if strings.TrimSpace(values[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	zone := strings.TrimSpace(values["CF_ZONE"])
	var err error
	if zone != "" {
		zone, err = domain.NormalizeHostname(zone)
		if err != nil {
			return Config{}, fmt.Errorf("CF_ZONE: %w", err)
		}
	}
	line := strings.TrimSpace(values["DNSPOD_RECORD_LINE"])
	if line == "" {
		line = "默认"
	}
	return Config{
		CFAPIToken:       strings.TrimSpace(values["CF_API_TOKEN"]),
		CFZone:           zone,
		DNSPodSecretID:   strings.TrimSpace(values["DNSPOD_SECRET_ID"]),
		DNSPodSecretKey:  strings.TrimSpace(values["DNSPOD_SECRET_KEY"]),
		DNSPodRecordLine: line,
	}, nil
}

func (c Config) Redact(message string) string {
	secrets := []string{c.CFAPIToken, c.DNSPodSecretID, c.DNSPodSecretKey}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}
