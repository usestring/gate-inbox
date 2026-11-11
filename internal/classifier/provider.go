package classifier

import (
	"net/url"
	"os"
	"strings"
)

type Provider string

const (
	// The zero value preserves JEV for boards without a saved provider setting.
	JEV             Provider = ""
	Clef            Provider = "clef"
	ProviderSetting          = "classifier_provider"
	JEVEndpoint              = "https://api.typesafe.ai/v1/systemone"
	JEVModel                 = "jev-1.13.0"
	ClefModel                = "clef"
	WorkerModel              = "@cf/cloudflare/clef"
	WorkerURLEnv             = "WORKERS_AI_URL"
)

func Parse(value string) Provider {
	if value == "jev" {
		return JEV
	}
	return Provider(value)
}

func (p Provider) Name() string {
	switch p {
	case JEV:
		return "JEV"
	case Clef:
		return "Clef"
	default:
		return "unknown classifier"
	}
}

func (p Provider) KeyEnv() string {
	switch p {
	case JEV:
		return "TYPESAFE_API_KEY"
	case Clef:
		return "WORKERS_API_KEY"
	default:
		return ""
	}
}

func (p Provider) Endpoint() string {
	switch p {
	case JEV:
		return JEVEndpoint
	case Clef:
		u, err := url.Parse(strings.TrimSpace(os.Getenv(WorkerURLEnv)))
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
			(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		u.Path = "/models"
		return u.String()
	default:
		return ""
	}
}
