package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

const (
	// DefaultServerTimeout is the default client request timeout used by the proxy server.
	DefaultServerTimeout = 60 * time.Second
	// DefaultBackendTimeout is the default timeout to use when connecting to the backend cloudpool.
	// Uses a rather conservative timeout since some cloudprovider operations may be quite
	// time-consuming (e.g. terminating a machine in Azure).
	DefaultBackendTimeout = 300 * time.Second
	// DefaultAPIServerTimeout is the default client timeout used when connecting to
	// Kubernetes' API server.
	DefaultAPIServerTimeout = 60 * time.Second
)

// Config describes the overall configuration of the proxy.
type ProxyConfig struct {
	APIServer APIServerConfig `json:"apiServer"`
	Backend   BackendConfig   `json:"backend"`
	Server    ServerConfig    `json:"server"`
}

// Validate validates the presence of all mandatory fields of a Config.
func (config *ProxyConfig) Validate() error {
	if err := config.APIServer.Validate(); err != nil {
		return fmt.Errorf("proxy config: %s", err)
	}

	if err := config.Backend.Validate(); err != nil {
		return fmt.Errorf("proxy config: backend: %s", err)
	}

	if err := config.Server.Validate(); err != nil {
		return fmt.Errorf("proxy config: server: %s", err)
	}

	return nil
}

func (config *ProxyConfig) String() string {
	bytes, _ := json.MarshalIndent(config, "", "  ")
	return string(bytes)
}

// APIServerConfig describes which Kubernetes API server to connect to and which credentials to use.
type APIServerConfig struct {
	// URL is the base address used to contact the API server. For example, https://master:6443
	URL  string              `json:"url"`
	Auth APIServerAuthConfig `json:"auth"`
	// Timeout is the connection timeout to use when contacting the backend.
	Timeout Duration `json:"timeout"`
}

// APIServerAuthConfig represents credentials used to authenticate against the Kubernetes API server.
type APIServerAuthConfig struct {
	// KubeConfigPath is a file system path to a kubeconfig file, the type of
	// configuration file that is used by `kubectl`. When specified, any other
	// auth fields are ignored (as they are all included in the kubeconfig).
	// The kubeconfig must contain cluster credentials for a cluster with the
	// specified API server URL.
	KubeConfigPath string `json:"kubeConfigPath"`
	// ClientCertPath is a file system path to a pem-encoded API server client/admin cert.
	// Ignored if KubeConfigPath is specified.
	ClientCertPath string `json:"clientCertPath"`
	// ClientKeyPath is a file system path to a pem-encoded API server client/admin key.
	// Ignored if KubeConfigPath is specified.
	ClientKeyPath string `json:"clientKeyPath"`
	// CACertPath is a file system path to a pem-encoded CA cert for the API server.
	// Ignored if KubeConfigPath is specified.
	CACertPath string `json:"caCertPath"`
}

// BackendConfig represents the backend cloudpool that the proxy sits in front of.
type BackendConfig struct {
	// URL is the base URL where the cloudpool REST API can be reached. For example, http://pool:9010.
	URL string `json:"url"`
	// Timeout is the connection timeout to use when contacting the backend.
	Timeout Duration `json:"timeout"`
}

// ServerConfig carries configuration for the proxy server itself.
type ServerConfig struct {
	// timeout is the client request timeout used by the proxy server. It defines the
	// maximum duration for reading an entire client request, including the body.
	Timeout Duration `json:"timeout"`
}

// NewFromJSON creates and validates a ProxyConfig from raw JSON format.
func NewFromJSON(rawJSON []byte) (*ProxyConfig, error) {
	var config ProxyConfig
	err := json.Unmarshal(rawJSON, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy config: %s", err)
	}
	if err = config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid proxy configuration: %s", err)
	}

	// populate defaults
	if config.Server.Timeout.Duration == 0*time.Second {
		config.Server.Timeout = Duration{DefaultServerTimeout}
	}
	if config.APIServer.Timeout.Duration == 0*time.Second {
		config.APIServer.Timeout = Duration{DefaultAPIServerTimeout}
	}
	if config.Backend.Timeout.Duration == 0*time.Second {
		config.Backend.Timeout = Duration{DefaultBackendTimeout}
	}

	return &config, nil
}

// Validate validates the presence of mandatory values in an APIServerConfig
func (config *APIServerConfig) Validate() error {
	if config.URL == "" {
		return fmt.Errorf("apiServer: missing field: url")
	}
	if _, err := url.Parse(config.URL); err != nil {
		return fmt.Errorf("apiServer: url: %s", err)
	}

	if err := config.Auth.Validate(); err != nil {
		return fmt.Errorf("apiServer: auth: %s", err)
	}

	return nil
}

// Validate validates the presence of mandatory values in an APIServerAuthConfig
func (config *APIServerAuthConfig) Validate() error {
	if config.KubeConfigPath != "" {
		return nil
	}

	if config.ClientCertPath == "" {
		return fmt.Errorf("missing value: clientCertPath")
	}
	if config.ClientKeyPath == "" {
		return fmt.Errorf("missing value: clientKeyPath")
	}
	if config.CACertPath == "" {
		return fmt.Errorf("missing value: caCertPath")
	}
	return nil
}

// Validate validates the presence of mandatory values in an BackendConfig
func (config *BackendConfig) Validate() error {
	if config.URL == "" {
		return fmt.Errorf("backend: missing field: url")
	}
	if _, err := url.Parse(config.URL); err != nil {
		return fmt.Errorf("backend: url: %s", err)
	}

	return nil
}

// Validate validates the presence of mandatory values in a ServerConfig
func (c *ServerConfig) Validate() error {
	return nil
}

// Duration is a wrapper type for JSON (un)marshalling of time.Duration
type Duration struct {
	time.Duration
}

// UnmarshalJSON implements the json.Unmarshaler interface for Duration.
func (d *Duration) UnmarshalJSON(b []byte) (err error) {
	sd := string(b[1 : len(b)-1])
	d.Duration, err = time.ParseDuration(sd)
	return
}

// MarshalJSON implements the json.Marshaler interface for Duration.
func (d Duration) MarshalJSON() (b []byte, err error) {
	return []byte(fmt.Sprintf(`"%s"`, d.String())), nil
}
