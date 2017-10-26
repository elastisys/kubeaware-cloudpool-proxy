package main

import (
	"flag"
	"io/ioutil"
	"os"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube"

	"github.com/golang/glog"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/proxy"
)

var (
	configPath string
	port       int
)

func init() {
	const (
		defaultConfigPath = "/etc/elastisys/kubeaware-cloudpool-proxy/config.json"
		defaultPort       = 8080
	)
	flag.StringVar(&configPath, "config-file", defaultConfigPath, "JSON-formatted configuration file.")

	// make glog write to stderr by default by setting --logtostderr to true
	flag.Lookup("logtostderr").Value.Set("true")
	// default glog verbosity level is 0 (higher values produce more output)
	flag.Lookup("v").Value.Set("0")
}

func main() {
	flag.Parse()

	if len(flag.Args()) < 1 {
		glog.Exitf("error: no machine to terminate given")
	}
	machineToTerminate := flag.Args()[0]

	// make sure any buffered output gets written when we exit
	defer glog.Flush()

	configFile, err := os.Open(configPath)
	if err != nil {
		glog.Exitf("failed to open configuration file: %s", err)
	}
	defer configFile.Close()

	configBytes, err := ioutil.ReadAll(configFile)
	if err != nil {
		glog.Exitf("error: could not read config: %s\n", err)
	}
	proxyConfig, err := config.NewFromJSON(configBytes)
	if err != nil {
		glog.Exitf("error: could not parse config: %s\n", err)
	}

	cloudPoolClient := cloudpool.NewClient(proxyConfig.Backend.URL, proxyConfig.Backend.Timeout.Duration)
	kubeClient, err := kube.NewKubeClient(&proxyConfig.APIServer)
	if err != nil {
		glog.Exitf("failed to create kubernetes client: %s", err)
	}
	nodeScaler := kube.NewNodeScaler(kubeClient)

	poolProxy := proxy.New(cloudPoolClient, nodeScaler)

	err = poolProxy.TerminateMachine(machineToTerminate, true)
	if err != nil {
		glog.Exitf("failed to terminate machine: %s", err)
	}
}
