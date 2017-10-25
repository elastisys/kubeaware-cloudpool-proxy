package kube

import (
	"fmt"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
	"k8s.io/client-go/kubernetes"
	kuberest "k8s.io/client-go/rest"

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

	kubeConfig := &kuberest.Config{
		Host: config.URL,
		TLSClientConfig: kuberest.TLSClientConfig{
			CertFile: config.Auth.ClientCertPath,
			KeyFile:  config.Auth.ClientKeyPath,
			CAFile:   config.Auth.CACertPath,
		},
		Timeout: config.Timeout.Duration,
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("could not create kubernetes client: %s", err)
	}

	return &DefaultKubeClient{api: kubeClient}, nil
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
