package config

import (
	"reflect"
	"testing"
	"time"
)

var (
	CompleteConfigJSON = `
{
	"server": {
		"timeout": "123s"
	},
  
	"apiServer": {
		"url": "http://sample.apiserver:6443",
		"auth": {
		  "clientCertPath": "/etc/ssl/admin.pem",
		  "clientKeyPath": "/etc/ssl/admin-key.pem",
		  "caCertPath": "/etc/ssl/ca.pem"
		},
		"timeout": "5m"
	},
  
	"backend": {
		"url": "http://sample.cloudpool",
		"timeout": "1m35s"
	}
}
`

	// expected struct
	CompleteConfig = &ProxyConfig{
		Server: ServerConfig{Timeout: Duration{123 * time.Second}},
		APIServer: APIServerConfig{
			URL: "http://sample.apiserver:6443",
			Auth: APIServerAuthConfig{
				ClientCertPath: "/etc/ssl/admin.pem",
				ClientKeyPath:  "/etc/ssl/admin-key.pem",
				CACertPath:     "/etc/ssl/ca.pem"},
			Timeout: Duration{5 * time.Minute},
		},
		Backend: BackendConfig{URL: "http://sample.cloudpool", Timeout: Duration{95 * time.Second}},
	}
)

var (
	MinimalConfigJSON = `
{
	"apiServer": {
		"url": "http://sample.apiserver:6443",
		"auth": {
		  "clientCertPath": "/etc/ssl/admin.pem",
		  "clientKeyPath": "/etc/ssl/admin-key.pem",
		  "caCertPath": "/etc/ssl/ca.pem"
		}
	},
  
	"backend": {
		"url": "http://sample.cloudpool"
	}
}
`
	// expected struct
	MinimalConfig = &ProxyConfig{
		Server: ServerConfig{Timeout: Duration{DefaultServerTimeout}},
		APIServer: APIServerConfig{
			URL: "http://sample.apiserver:6443",
			Auth: APIServerAuthConfig{
				ClientCertPath: "/etc/ssl/admin.pem",
				ClientKeyPath:  "/etc/ssl/admin-key.pem",
				CACertPath:     "/etc/ssl/ca.pem"},
			Timeout: Duration{DefaultAPIServerTimeout},
		},
		Backend: BackendConfig{URL: "http://sample.cloudpool", Timeout: Duration{DefaultBackendTimeout}},
	}
)

// illegal apiserver URL
var MalformedURLJSON = `
{
	"apiServer": {
		"url": ":",
		"auth": {
		  "clientCertPath": "/etc/ssl/admin.pem",
		  "clientKeyPath": "/etc/ssl/admin-key.pem",
		  "caCertPath": "/etc/ssl/ca.pem"
		}
	},
  
	"backend": {
		"url": "ftp://sample.cloudpool"
	}
}
`

var IllegalTimeoutJSON = `
{
	"apiServer": {
		"url": "http://sample.apiserver:6443",
		"auth": {
		  "clientCertPath": "/etc/ssl/admin.pem",
		  "clientKeyPath": "/etc/ssl/admin-key.pem",
		  "caCertPath": "/etc/ssl/ca.pem"
		}
	},
  
	"backend": {
		"url": "http://sample.cloudpool",
		"timeout": "10-years"
	}
}
`

// ensure that JSON configurations are properly parsed and converted to their struct counterparts
func TestNewFromJSON(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      *ProxyConfig
		errorExpected bool
	}{
		{name: "complete config", input: CompleteConfigJSON, expected: CompleteConfig, errorExpected: false},
		// verifies default values
		{name: "minimal config", input: MinimalConfigJSON, expected: MinimalConfig, errorExpected: false},
		// check URLs
		{name: "malformed apiserver url", input: MalformedURLJSON, expected: nil, errorExpected: true},
		// parsing of duration
		{name: "illegal duration", input: IllegalTimeoutJSON, expected: nil, errorExpected: true},
	}

	for _, test := range tests {
		got, err := NewFromJSON([]byte(test.input))
		if test.errorExpected {
			if err == nil {
				t.Errorf("%s: expected to fail", test.name)
			}
		} else {
			if err != nil {
				t.Errorf("%s: not expected to fail. failed with: %s", test.name, err)
			}
			if !reflect.DeepEqual(got, test.expected) {
				t.Errorf("%s: expected %s, got: %s", test.name, test.expected, got)
			}
		}
	}

}
