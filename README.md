[![Go Report Card](https://goreportcard.com/badge/github.com/elastisys/kubeaware-cloudpool-proxy)](https://goreportcard.com/report/github.com/elastisys/kubeaware-cloudpool-proxy)
[![Build Status](https://travis-ci.org/elastisys/kubeaware-cloudpool-proxy.svg?branch=master)](https://travis-ci.org/elastisys/kubeaware-cloudpool-proxy)
[![Coverage](https://codecov.io/gh/elastisys/kubeaware-cloudpool-proxy/branch/master/graph/badge.svg)](https://codecov.io/gh/elastisys/kubeaware-cloudpool-proxy)



# kubeaware-cloudpool-proxy
The `kubeaware-cloudpool-proxy` is a proxy that is placed between a
[cloudpool](http://cloudpoolrestapi.readthedocs.io) and its clients (for example, an autoscaler).
In essence, the `kubeaware-cloudpool-proxy` adds Kubernetes-awareness to an existing cloudpool
implementation. The Kubernetes-awareness allows worker node scale-downs to be handled with less
disruption by taking the current Kubernetes cluster state into account, carefully selecting a node,
and evacuating its pods prior to terminating the cloud machine instead of just brutally killing a "random" worker node (at least appearing "random" from the Kubernetes-perspective).

The kubeaware-cloudpool-proxy delegates all cloud-specific actions to its backend cloudpool.
In fact, most REST API operations are directly forwarded to the backend cloudpool as-is. There
are two notable exceptions, that require the proxy to take action, both of which could lead to
a scale-down:

- [set desired size](http://cloudpoolrestapi.readthedocs.io/en/latest/api.html#set-desired-size):
  If a scale-down is suggested (`desiredSize` lower than the current pool size),
  victims need to be carefully selected and gracefully shut down (see below).
- [terminate machine](http://cloudpoolrestapi.readthedocs.io/en/latest/api.html#terminate-machine):
  Is only allowed if the machine is a viable scale-down victim and if so, the machine
  needs to be gracefully shut down (see below).

When a node needs to be removed, the `kubeaware-cloudpool-proxy` communicates with
the Kubernetes API server to determine the current cluster state. These interactions
are illustrated in the image below.

![architecture](/docs/images/architecture.svg)

When asked to scale down, the `kubeaware-cloudpool-proxy` takes care of taking down
nodes in a controlled manner by:

- Carefully determining which (if any) nodes are candidates for being removed.
  A node qualifies as a scale-down candidate if it satisfies all of the
  following conditions:
  - the node must not be protected with a `cluster-autoscaler.kubernetes.io/scale-down-disabled` annotation.
  - the node must not be a master node (as indicated by it running a pod in namespace `kube-system` named
    `kube-apiserver-<host>` or having a `component` label with value `kube-apiserver`)
  - there must be other remaining non-master nodes that are `Ready` and `Schedulable`
  - the node's pods must be possible to evacuate to the remaining nodes:
    - the sum of pod-requested CPU/memory on the node must not exceed free space on remaining nodes
    - the node must not have any pods without [controller](https://kubernetes.io/docs/concepts/workloads/controllers/replicationcontroller/) (such as deployment/replication controller),
      since such pods would not be recreated on a different node when evicted.
    - the node must not have any pods with (node-)local storage
    - the node must not have pods with a [pod disruption budget](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/) that would be violated
    - [taints](https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/) on the remaining nodes must not prevent the node's pods from being evacuated
      (the pods must have matching [tolerations](https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/) for such cases)
    - the node pods must not have [node selectors](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/) that prevent them from being moved
    - the node pods must not have [node-affinity constraints](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/) that prevent them from being moved

- Selecting the "best" victim node to kill (if at least one candidate was found
  in the prior step). In this context, the "best" node is typically the least
  loaded node -- the node with the least amount of pods that need to be evacuated
  to another node.
- If a victim node is found, it needs to be evacuated before it can be killed. This happens as follows:
  - The node is marked unschedulable via a node taint (to avoid new pods being scheduled onto the node).
  - The node is drained: all non-system pods are evicted (and will be rescheduled to the remaining nodes).
  - The node is deleted from the Kubernetes cluster.
  - Finally, the node is terminated in the cloud through the
    [terminate machine](http://cloudpoolrestapi.readthedocs.io/en/latest/api.html#terminate-machine)
    call to the backend cloudpool.


## Building

`build.sh` builds the binary and runs all tests (`build.sh --help` for build options).

The built binary is placed under `bin`. The main binary is `kubeaware-cloudpool-proxy`.

Test coverage output is placed under `build/coverage/` and can be viewed as HTML
via:

    go tool cover -html build/coverage/<package>.out



## Configuring
The `kubeaware-cloudpool-proxy` requires a JSON-formatted configuration file.
It has the following structure:

```
{
  "server": {
      "timeout": "60s"
  },

  "apiServer": {
      "url": "https://<host>:<port>",
      "auth": {
        "clientCertPath": "/path/to/admin.pem",
        "clientKeyPath": "/path/to/admin-key.pem",
        "caCertPath": "/path/to/ca.pem",
      },
      "timeout": "10s",
  },

  "backend": {
      "cloudPoolUrl": "http://<host>:<port>",
      "timeout": "300s",
  }

}
```

The authentication part can be specified either with a concrete client
certicate/key pair and a CA cert or via a
[kubeconfig](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/) file.

With a kubeconfig file, the `auth` is specified as follows:

```
...
  "apiServer": {
      "url": "https://<host>:<port>",
      "auth": {
        "kubeConfigPath": "/home/me/.kube/config"
      }
  },
...
```

With a specific client cert/key the `auth` configuration looks as follows:

```
...
  "apiServer": {
      "url": "https://<host>:<port>",
      "auth": {
        "clientCertPath": "/path/to/admin.pem",
        "clientKeyPath": "/path/to/admin-key.pem",
        "caCertPath": "/path/to/ca.pem",
      }
  },
...
```



The fields carry the following semantics:

  - `server`: proxy server settings
    - `timeout`: read timeout on client requests. Default: `60s`
  - `apiServer`: settings for the Kubernets API server
    - `url`: URL is the base address used to contact the API server. For example, `https://master:6443`.
    - `auth`: client authentication credentials
      - `kubeConfigPath`: a file system path to a kubeconfig file, the type of
        configuration file that is used by `kubectl`. When specified, any other
		auth fields are ignored (as they are all included in the kubeconfig).
		The kubeconfig must contain cluster credentials for a cluster with an
		API server with the specified `url`.
      - `clientCertPath`: a file system path to a pem-encoded API server
        client/admin cert. Ignored if `kubeConfigPath` is specified.
      - `clientKeyPath`: a file system path to a pem-encoded API server
        client/admin key.  Ignored if `kubeConfigPath` is specified.
      - `caCertPath`: a file system path to a pem-encoded CA cert for the API
        server.  Ignored if `kubeConfigPath` is specified.
    - `timeout`: request timeout used when communicating with the API server. Default: `60s`.
  - `backend`: settings for communicating with the backend cloudpool that the proxy sits in front of.
    - `cloudPoolUrl`: the base URL where the
      [cloudpool REST API](http://cloudpoolrestapi.readthedocs.io/en/latest/api.html) can be reached.
      For example, `http://cloudpool:9010`.
    - `timeout`: the connection timeout to use when contacting the backend. Default: `300s`.
      *Note: you may need to set a quite substantial timeout for the backend since some cloudprovider operations may be quite time-consuming (e.g. terminating a machine in Azure)*

## Running

After building, run the proxy via:

    ./bin/kubeaware-cloudpool-proxy --config-file=<path>

To enable a different [glog](https://github.com/golang/glog) log level use something like:

    ./bin/kubeaware-cloudpool-proxy --config-file=<path> --v=4


## Docker
To build a docker image, run

    ./build.sh --docker

To run the docker image, run something similar to:

    docker run --rm -p 8080:8080 \
       -v <config-dir>:/etc/elastisys \
       -v <kubessl-dir>:/etc/kubessl \
       elastisys/kubeaware-cloudpool-proxy:1.0.0 \
       --config-file=/etc/elastisys/config.json --port 8080

In this example, `<config-dir>` is a host directory that contains a `config.json` file
for the `kubeaware-cloudpool-proxy`. Furthermore, `<kubessl-dir>` must contain the
pem-encoded certificate/key/CA files required to talk to the Kubernetes API server.
These cert files are referenced from the `config.json` which, in this case, could look
something like:

```
{
    "apiServer": {
        "url": "https://<hostname>",
        "auth": {
            "clientCertPath": "/etc/kubessl/admin.pem",
            "clientKeyPath": "/etc/kubessl/admin-key.pem",
            "caCertPath": "/etc/kubessl/ca.pem"
        }
    },
    "backend": {
        "url": "http://<hostname>:9010",
        "timeout": "10s"
    }
}
```

## Developer notes

### Dependencies
[dep](https://github.com/golang/dep) is used for dependency management.
Make sure it is [installed](https://github.com/golang/dep/releases).

To introduce a new dependency, add it to `Gopkg.toml`, edit some piece of
code to import a package from the dependency, and then run:

    dep ensure

to get the right version into the `vendor` folder.


### Testing
The regular `go test` command can be used for testing.

To test a certain package, and to see logs (for a certain glog v-level), run something like:

    go test -v ./pkg/kube -args -v=4 -logtostderr=true

For some tests, mock clients are used to fake interactions with "backend services".
More specifically, these interfaces are `KubeClient`, `CloudPoolClient`, and
`NodeScaler`. Should any of these interfaces change, the mocks
need to be recreated (before editing the test code to modify expectations, etc).
This can be achieved via the [mockery](https://github.com/vektra/mockery) tool.

  1. Installing mockery: `go get github.com/vektra/mockery/...`
  2. Generating the mocks

         mockery -dir pkg/kube/ -name KubeClient -output pkg/kube/mocks

         mockery -dir pkg/kube/ -name NodeScaler -output pkg/proxy/mocks
         mockery -dir pkg/cloudpool/ -name CloudPoolClient -output pkg/proxy/mocks


      The generated mocks should end up under `pkg/mocks/`

### Useful references

- [1] [kubectl drain code](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubectl/cmd/drain.go)
- [2] [cluster autoscaler scale-down code](https://github.com/kubernetes/autoscaler/blob/cluster-autoscaler-1.0.0/cluster-autoscaler/core/scale_down.go)
- [3] [kubernetes scheduler overview](https://github.com/kubernetes/community/blob/8decfe42b8cc1e027da290c4e98fa75b3e98e2cc/contributors/devel/scheduler.md)
- [4] [kubernetes scheduler predicates code](https://github.com/kubernetes/kubernetes/blob/v1.8.2/plugin/pkg/scheduler/algorithm/predicates/predicates.go)

### Ideas for future work
In some cases, we would like to see more rapid utilization of newly introduced worker nodes,
to make sure that it immediately starts accepting a share of the workload. Typically,
what we've seen so far, is that a new node gets started, but once it is up it is typically
very lightly loaded (if at all). It would be nice to see some pods being pushed over to the
node. Furthermore, it would be useful to make sure that all required docker images are
pulled to new nodes as early as possible to avoid unnecssary delays later when pods are
scheduled onto the node.
