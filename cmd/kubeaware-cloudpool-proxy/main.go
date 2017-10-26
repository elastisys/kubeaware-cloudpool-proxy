package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube"

	"github.com/golang/glog"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/proxy"
)

// version is the release version of the program. This is intended to be
// set by the linker at build-time
var version string

// Command-line options
var (
	configPath  string
	port        int
	showVersion bool
)

func init() {
	const (
		defaultConfigPath = "/etc/elastisys/kubeaware-cloudpool-proxy.json"
		defaultPort       = 8080
	)
	flag.StringVar(&configPath, "config-file", defaultConfigPath, "JSON-formatted configuration file.")
	flag.IntVar(&port, "port", defaultPort, "Port used to serve REST API")
	flag.BoolVar(&showVersion, "version", false, "Show version")

	// make glog write to stderr by default by setting --logtostderr to true
	flag.Lookup("logtostderr").Value.Set("true")
	// default glog verbosity level is 0 (higher values produce more output)
	flag.Lookup("v").Value.Set("0")
}

func main() {
	flag.Parse()

	// make sure any buffered output gets written when we exit
	defer glog.Flush()

	if showVersion {
		fmt.Printf("Version: %s\n", version)
		os.Exit(0)
	}

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
	restAPI := proxy.NewServer(poolProxy, port, proxyConfig.Server.Timeout.Duration)

	glog.Infof("backend cloudpool: %s\n", proxyConfig.Backend.URL)
	glog.Infof("server listening on port %d ...\n", port)
	glog.Fatal(restAPI.ListenAndServe())
}
