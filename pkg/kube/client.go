package kube

import (
	"fmt"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
	"k8s.io/client-go/kubernetes"
	kuberest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	policy "k8s.io/client-go/pkg/apis/policy/v1beta1"
)

// KubeClient represents a kubernetes client that supports a small
// subset of the full kubernetes API required by the NodeScaler.
type KubeClient interface {
	ListNodes() (*apiv1.NodeList, error)
	GetNode(name string) (*apiv1.Node, error)
	UpdateNode(node *apiv1.Node) error
	DeleteNode(node *apiv1.Node) error

	ListPods(namespace string, options metav1.ListOptions) (*apiv1.PodList, error)
	EvictPod(eviction *policy.Eviction) error

	PodDisruptionBudgets(namespace string) (*policy.PodDisruptionBudgetList, error)
}

// NewKubeClient creates a new Kubernetes API client with a given configuration.
func NewKubeClient(config *config.APIServerConfig) (KubeClient, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("API server config is invalid: %s", err)
	}

	var err error
	var tlsConfig *kuberest.TLSClientConfig

	if config.Auth.KubeConfigPath != "" {
		kubeConfig, err := clientcmd.LoadFromFile(config.Auth.KubeConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig %s: %s", config.Auth.KubeConfigPath, err)
		}
		tlsConfig, err = tlsClientConfigFromKubeConfig(kubeConfig, config.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to load auth credentials from kubeconfig %s: %s",
				config.Auth.KubeConfigPath, err)
		}
	} else {
		tlsConfig = &kuberest.TLSClientConfig{
			CertFile: config.Auth.ClientCertPath,
			KeyFile:  config.Auth.ClientKeyPath,
			CAFile:   config.Auth.CACertPath,
		}
	}

	clientConfig := &kuberest.Config{
		Host:            config.URL,
		TLSClientConfig: *tlsConfig,
		Timeout:         config.Timeout.Duration,
	}

	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("could not create kubernetes client: %s", err)
	}

	return &DefaultKubeClient{api: kubeClient}, nil
}

// tlsClientConfigFromKubeConfig extracts client TLS credentials from
// a kubeconfig file for a given cluster API server. If no such cluster
// credentials can be found, an error is returned.
func tlsClientConfigFromKubeConfig(kubeconfig *clientcmdapi.Config, apiServerURL string) (*kuberest.TLSClientConfig, error) {
	tlsConfig := kuberest.TLSClientConfig{}

	// first find the cluster in kubeconfig with matching api server
	// (it holds the apiserver's ca cert)
	var kubeconfigClusterName string
	for clusterName, cluster := range kubeconfig.Clusters {
		if cluster.Server == apiServerURL {
			kubeconfigClusterName = clusterName
			// may either be specified as file path or data
			tlsConfig.CAFile = cluster.CertificateAuthority
			tlsConfig.CAData = cluster.CertificateAuthorityData
			break
		}
	}

	if kubeconfigClusterName == "" {
		return nil, fmt.Errorf("kubeconfig does not contain a cluster with api server %s", apiServerURL)
	}

	// next find a context associated with cluster
	var contextUser string
	for _, context := range kubeconfig.Contexts {
		if context.Cluster == kubeconfigClusterName {
			contextUser = context.AuthInfo
		}
	}
	if contextUser == "" {
		return nil, fmt.Errorf("kubeconfig does not contain a context for a cluster with api server %s", apiServerURL)
	}

	// next find the user matching the context (it holds the client cert and key)
	for userName, user := range kubeconfig.AuthInfos {
		if userName == contextUser {
			// may either be specified as file path or data
			tlsConfig.CertFile = user.ClientCertificate
			tlsConfig.CertData = user.ClientCertificateData
			tlsConfig.KeyFile = user.ClientKey
			tlsConfig.KeyData = user.ClientKeyData
			return &tlsConfig, nil
		}
	}

	return nil, fmt.Errorf("kubeconfig does not contain a user named %s", contextUser)
}

// DefaultKubeClient is an implementation of the KubeClient interface.
type DefaultKubeClient struct {
	api kubernetes.Interface
}

func (c *DefaultKubeClient) ListNodes() (*apiv1.NodeList, error) {
	return c.api.Core().Nodes().List(metav1.ListOptions{})
}

func (c *DefaultKubeClient) GetNode(name string) (*apiv1.Node, error) {
	return c.api.Core().Nodes().Get(name, metav1.GetOptions{})
}
func (c *DefaultKubeClient) UpdateNode(node *apiv1.Node) error {
	_, err := c.api.Core().Nodes().Update(node)
	return err
}

func (c *DefaultKubeClient) DeleteNode(node *apiv1.Node) error {
	return c.api.Core().Nodes().Delete(node.Name, &metav1.DeleteOptions{})
}

func (c *DefaultKubeClient) ListPods(namespace string, options metav1.ListOptions) (*apiv1.PodList, error) {
	return c.api.Core().Pods(namespace).List(options)
}

func (c *DefaultKubeClient) EvictPod(eviction *policy.Eviction) error {
	return c.api.Core().Pods(eviction.ObjectMeta.Namespace).Evict(eviction)
}

func (c *DefaultKubeClient) PodDisruptionBudgets(namespace string) (*policy.PodDisruptionBudgetList, error) {
	return c.api.Policy().PodDisruptionBudgets(namespace).List(metav1.ListOptions{})
}
