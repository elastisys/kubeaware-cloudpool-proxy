package main

import (
	"flag"
	"time"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"

	"github.com/golang/glog"
)

var (
	cloudPoolURL string
)

func init() {
	flag.StringVar(&cloudPoolURL, "cloudpool-url", "", "Cloudpool base URL.")

	// make glog write to stderr by default by setting --logtostderr to true
	flag.Lookup("logtostderr").Value.Set("true")
	// default glog verbosity level is 0 (higher values produce more output)
	flag.Lookup("v").Value.Set("0")
}

func main() {
	flag.Parse()

	// make sure any buffered output gets written when we exit
	defer glog.Flush()

	if cloudPoolURL == "" {
		glog.Exitf("error: no --cloudpool-url given")
	}

	client := cloudpool.NewClient(cloudPoolURL, 0*time.Second)

	poolSize, err := client.GetPoolSize()
	if err != nil {
		glog.Exitf("failed to get machine pool size: %s", err)
	}
	glog.V(0).Infof("machine pool size: %s", poolSize)

	machinePool, err := client.GetMachinePool()
	if err != nil {
		glog.Exitf("failed to get machine pool: %s", err)
	}
	glog.V(0).Infof("got machine pool: %v", machinePool)

	err = client.SetDesiredSize(poolSize.DesiredSize)
	if err != nil {
		glog.Exitf("failed to set desired size: %s", err)
	}

	for _, m := range machinePool.Machines {
		glog.Infof("terminating machine %s ...", m.ID)
		err = client.TerminateMachine(m.ID, true)
		if err != nil {
			glog.Exitf("failed to terminate machine: %s", err)
		}
	}
}
