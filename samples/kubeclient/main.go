package main

import (
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"

	"github.com/golang/glog"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube"
)

var (
	apiServerURL   string
	kubeConfigFile string
	certFile       string
	certKeyFile    string
	caCertFile     string
)

func init() {
	defaultKubeConfigFile := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	flag.StringVar(&apiServerURL, "apiserver-url", "", "Kubernetes API server base URL.")
	flag.StringVar(&kubeConfigFile, "kubeconfig", defaultKubeConfigFile, "kubeconfig file.")
	flag.StringVar(&certFile, "cert-file", "", "Client certificate.")
	flag.StringVar(&certKeyFile, "cert-key", "", "Client private key.")
	flag.StringVar(&caCertFile, "ca-cert", "", "CA certificate.")

	// make glog write to stderr by default by setting --logtostderr to true
	flag.Lookup("logtostderr").Value.Set("true")
	// default glog verbosity level is 0 (higher values produce more output)
	flag.Lookup("v").Value.Set("0")
}

func main() {
	flag.Parse()

	// make sure any buffered output gets written when we exit
	defer glog.Flush()

	if apiServerURL == "" {
		glog.Exitf("error: no --apiserver-url given")
	}

	conf := &config.APIServerConfig{
		URL: apiServerURL,
		Auth: config.APIServerAuthConfig{
			KubeConfigPath: kubeConfigFile,
			ClientCertPath: certFile,
			ClientKeyPath:  certKeyFile,
			CACertPath:     caCertFile,
		},
		Timeout: config.Duration{Duration: 10 * time.Second},
	}

	kubeclient, err := kube.NewKubeClient(conf)
	if err != nil {
		glog.Exitf("could not create kubernetes client: %s\n", err)
	}

	nodeList, err := kubeclient.ListNodes()
	if err != nil {
		glog.Exitf("failed to list nodes: %s", err)
	}
	for _, node := range nodeList.Items {
		glog.Infof("node: %s", node.Name)
	}
}
