package kube

import (
	"testing"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"

	"github.com/stretchr/testify/assert"
)

// paths to sample certificates
var (
	clientCertPath string = "./testpki/client-cert.pem"
	clientKeyPath  string = "./testpki/client-key.pem"
	caCertPath     string = "./testpki/ca.pem"

	invalidClientCertPath string = "./testpki/bad-client-cert.pem"
	invalidClientKeyPath  string = "./testpki/bad-client-key.pem"
	invalidCaCertPath     string = "./testpki/bad-ca.pem"
)

// It should be possible to create a client by specifying explicit client cert/key,
// and CA cert paths.
func TestClientFromCertPaths(t *testing.T) {
	config := &config.APIServerConfig{
		URL: "https://api.server:6443",
		Auth: config.APIServerAuthConfig{
			ClientCertPath: clientCertPath,
			ClientKeyPath:  clientKeyPath,
			CACertPath:     caCertPath,
		},
	}
	client, err := NewKubeClient(config)
	assert.NotNil(t, client, "expected client creation to be successful")
	assert.Nil(t, err, "did not expect an error to occur")
}

// Client creation should fail when invalid paths are specified
func TestClientFromCertPathsOnFailure(t *testing.T) {
	tests := []struct {
		certPath       string
		keyPath        string
		caPath         string
		expectedToFail bool
	}{
		{certPath: invalidClientCertPath, keyPath: clientKeyPath, caPath: caCertPath, expectedToFail: true},
		{certPath: clientCertPath, keyPath: invalidClientKeyPath, caPath: caCertPath, expectedToFail: true},
		{certPath: clientCertPath, keyPath: clientKeyPath, caPath: invalidCaCertPath, expectedToFail: true},
	}

	for _, test := range tests {
		config := &config.APIServerConfig{URL: "https://apiserver:6443",
			Auth: config.APIServerAuthConfig{
				ClientCertPath: test.certPath, ClientKeyPath: test.keyPath, CACertPath: test.caPath},
		}
		client, err := NewKubeClient(config)
		if test.expectedToFail {
			assert.Nil(t, client, "expected client creation to fail")
			assert.NotNil(t, err, "expected to fail")
		} else {
			assert.NotNil(t, client, "expected client creation to be successful")
			assert.Nil(t, err, "did not expect an error to occur")
		}
	}
}

// It should be possible to create a client from a kubeconfig file.
func TestClientFromKubeConfig(t *testing.T) {
	tests := []struct {
		name           string
		kubeConfigPath string
		expectedToFail bool
	}{
		// should be possible to specify certificates as paths
		{name: "cert-paths", kubeConfigPath: "./testpki/kubeconfig-certpath", expectedToFail: false},
		// should be possible to specify certificates as base64-encoded data
		{name: "cert-data", kubeConfigPath: "./testpki/kubeconfig-certdata", expectedToFail: false},
		// a kubeconfig that doesn't contain a cluster with expected api server
		{name: "wrong-apiserver", kubeConfigPath: "./testpki/kubeconfig-wrong-apiserver", expectedToFail: true},
		// cluster with right apiserver exists, but no context to tie cluster to user
		{name: "no-context", kubeConfigPath: "./testpki/kubeconfig-no-context", expectedToFail: true},
		// cluster with right apiserver exists and a context, but the user referenced by context
		// does not exist
		{name: "no-user", kubeConfigPath: "./testpki/kubeconfig-no-user", expectedToFail: true},
	}

	for _, test := range tests {
		config := &config.APIServerConfig{URL: "https://api.server:6443",
			Auth: config.APIServerAuthConfig{
				KubeConfigPath: test.kubeConfigPath,
			},
		}
		client, err := NewKubeClient(config)
		if test.expectedToFail {
			assert.Nil(t, client, "expected client creation to fail")
			assert.NotNil(t, err, "expected to fail")
		} else {
			assert.NotNil(t, client, "expected client creation to be successful")
			assert.Nil(t, err, "did not expect an error to occur")
		}
	}
}
