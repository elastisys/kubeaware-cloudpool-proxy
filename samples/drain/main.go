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
	apiServerURL string
	certFile     string
	certKeyFile  string
	caCertFile   string
)

func init() {
	defaultCertFile := filepath.Join(os.Getenv("HOME"), ".kube", "pki", "client-cert.pem")
	defaultCertKey := filepath.Join(os.Getenv("HOME"), ".kube", "pki", "client-key.pem")
	defaultCaCert := filepath.Join(os.Getenv("HOME"), ".kube", "pki", "ca.pem")

	flag.StringVar(&apiServerURL, "apiserver-url", "", "Kubernetes API server base URL.")
	flag.StringVar(&certFile, "cert-file", defaultCertFile, "Client certificate.")
	flag.StringVar(&certKeyFile, "cert-key", defaultCertKey, "Client private key.")
	flag.StringVar(&caCertFile, "ca-cert", defaultCaCert, "CA certificate.")

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

	if len(flag.Args()) < 1 {
		glog.Exitf("error: no node to drain given\n")
	}

	nodeToDrain := flag.Args()[0]

	nodeScaler := &kube.DefaultNodeScaler{KubeClient: kubeclient}
	victimNode, err := nodeScaler.GetNode(nodeToDrain)
	if err != nil {
		glog.Exitf("failed to get victim node: %s", err)
	}

	canBeScaledDown, err := nodeScaler.IsScaleDownCandidate(victimNode)
	if !canBeScaledDown {
		glog.Exitf("node %s cannot be safely scaled down", nodeToDrain)
	}
	glog.Infof("node %s is safe to scale down", nodeToDrain)
	err = nodeScaler.DrainNode(victimNode)
	if err != nil {
		glog.Errorf("failed to drain node %s: %s", nodeToDrain, err)
	}
}
