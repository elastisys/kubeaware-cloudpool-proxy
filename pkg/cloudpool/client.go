package cloudpool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"

	"github.com/golang/glog"
)

// HTTPForwarder is an interface for an entity capable of forwarding a
// HTTP request to a remote endpoint and returning its response.
type HTTPForwarder interface {
	Forward(request *http.Request) (*http.Response, error)
}

// CloudPoolClient is a cloudpool client interface that supports the subset of cloudpool
// operations required by the proxy.
type CloudPoolClient interface {
	HTTPForwarder

	// GetPoolSize represents a GET to /pool/size
	GetPoolSize() (*PoolSizeMessage, error)
	// GetMachinePool represents a GET to /pool
	GetMachinePool() (*MachinePoolMessage, error)
	// GetMachine retrieves a particular machine from the cloudpool
	GetMachine(machineID string) (*Machine, error)
	// SetDesiredSize represents a POST to /pool/size
	SetDesiredSize(desiredSize int) error
	// TerminateMachine represents a POST to /pool/<machineID>/terminate
	TerminateMachine(machineID string, decrementDesiredSize bool) error
}

// DefaultCloudPoolClient is a default Client implementation.
type DefaultCloudPoolClient struct {
	CloudPoolURL string
	Timeout      time.Duration
}

// NewClient creates a new cloudpool Client.
func NewClient(cloudPoolURL string, timeout time.Duration) *DefaultCloudPoolClient {
	if timeout <= 0*time.Second {
		timeout = config.DefaultBackendTimeout
	}
	return &DefaultCloudPoolClient{CloudPoolURL: cloudPoolURL, Timeout: timeout}
}

// GetPoolSize retrieves the current pool size from the backend cloudpool.
func (c *DefaultCloudPoolClient) GetPoolSize() (*PoolSizeMessage, error) {
	req, e := http.NewRequest("GET", c.poolURL("/pool/size"), nil)
	if e != nil {
		return nil, &ErrorMessage{Message: "GetPoolSize: failed to create request message", Detail: e.Error()}
	}

	bodyBytes, err := c.doRequest(req)
	if err != nil {
		return nil, ErrorWithCause(err, "getPoolSize")
	}

	var poolSize PoolSizeMessage
	e = json.Unmarshal(bodyBytes, &poolSize)
	if e != nil {
		return nil, &ErrorMessage{Message: "GetPoolSize: failed to unmarshal json response", Detail: e.Error()}
	}

	return &poolSize, nil
}

// GetMachinePool retrieves the machine pool from the backend cloudpool.
func (c *DefaultCloudPoolClient) GetMachinePool() (*MachinePoolMessage, error) {
	req, e := http.NewRequest("GET", c.poolURL("/pool"), nil)
	if e != nil {
		return nil, &ErrorMessage{Message: "GetMachinePool: failed to create request message", Detail: e.Error()}
	}

	bodyBytes, err := c.doRequest(req)
	if err != nil {
		return nil, ErrorWithCause(err, "getMachinePool")
	}

	var pool MachinePoolMessage
	e = json.Unmarshal(bodyBytes, &pool)
	if e != nil {
		return nil, &ErrorMessage{Message: "GetMachinePool: failed to unmarshal json response", Detail: e.Error()}
	}

	return &pool, nil
}

// GetMachine returns a machine with a particular machine ID from the pool.
func (c *DefaultCloudPoolClient) GetMachine(machineID string) (*Machine, error) {
	pool, err := c.GetMachinePool()
	if err != nil {
		return nil, ErrorWithCause(err, "getMachine %s: failed to get machine", machineID)
	}
	for _, machine := range pool.Machines {
		if machine.ID == machineID {
			return &machine, nil
		}
	}

	return nil, &ErrorMessage{Message: fmt.Sprintf("getMachine %s: machine not found", machineID)}
}

// SetDesiredSize sets the desired size on the backend cloudpool.
func (c *DefaultCloudPoolClient) SetDesiredSize(desiredSize int) error {
	messageBytes, e := json.Marshal(&SetDesiredSizeMessage{desiredSize})
	if e != nil {
		return &ErrorMessage{Message: "setDesiredSize: failed to encode request message", Detail: e.Error()}
	}

	req, e := http.NewRequest("POST", c.poolURL("/pool/size"), bytes.NewReader(messageBytes))
	if e != nil {
		return &ErrorMessage{Message: "setDesiredSize: failed to create request", Detail: e.Error()}
	}
	req.Header.Add("Content-Type", "application/json")

	_, err := c.doRequest(req)
	if err != nil {
		return ErrorWithCause(err, "setDesiredSize %d", desiredSize)
	}

	return nil
}

// TerminateMachine asks the backend cloudpool to terminate a particular machine,
// possibly also calling in a replacement (if decrementDesiredSize is false).
func (c *DefaultCloudPoolClient) TerminateMachine(machineID string, decrementDesiredSize bool) error {
	messageBytes, e := json.Marshal(&TerminateMachineMessage{machineID, decrementDesiredSize})
	if e != nil {
		return &ErrorMessage{Message: "terminateMachine: failed to encode request message", Detail: e.Error()}
	}

	url := c.poolURL(fmt.Sprintf("/pool/terminate"))
	req, e := http.NewRequest("POST", url, bytes.NewReader(messageBytes))
	if e != nil {
		return &ErrorMessage{Message: "terminateMachine: failed to create request", Detail: e.Error()}
	}
	req.Header.Add("Content-Type", "application/json")

	_, err := c.doRequest(req)
	if err != nil {
		return ErrorWithCause(err, "terminateMachine %s", machineID)
	}

	return nil
}

// doRequest performs a HTTP request and returns the response body bytes if
// everything was successful and an error otherwise
func (c *DefaultCloudPoolClient) doRequest(request *http.Request) ([]byte, error) {
	glog.V(2).Infof("sending request to %s", request.URL)
	resp, err := c.Forward(request)
	if err != nil {
		return nil, ErrorWithCause(err, "backend cloudpool request failed")
	}
	defer resp.Body.Close()

	bodyBytes, e := ioutil.ReadAll(resp.Body)
	if e != nil {
		return nil, &ErrorMessage{Message: "failed to read response body", Detail: e.Error()}
	}

	// handle error responses
	if resp.StatusCode != http.StatusOK {
		var errMsg ErrorMessage
		err := json.Unmarshal(bodyBytes, &errMsg)
		if err != nil {
			return nil, &ErrorMessage{
				Message: fmt.Sprintf("backend cloudpool responded with HTTP status: %s", resp.Status),
				Detail:  string(bodyBytes),
			}
		}
		return nil, &errMsg
	}

	glog.V(4).Infof("server response: %s", bodyBytes)

	return bodyBytes, nil
}

// Forward attempts to forward a generic HTTP request to the backend cloudpool.
// The request URL will be modified to only keep the path part of the URL and
// append it to the configured `cloudPoolURL`.
func (c *DefaultCloudPoolClient) Forward(req *http.Request) (*http.Response, error) {
	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: c.Timeout}

	// set up identical request to be forwarded to cloudpool
	url := c.poolURL(req.URL.Path)
	forwardReq, err := http.NewRequest(req.Method, url, req.Body)
	if err != nil {
		return nil, &ErrorMessage{Message: "failed to create request to forward", Detail: err.Error()}
	}
	forwardReq.Header = req.Header
	forwardReq.Header.Add("X-Forwarded-For", strings.Split(req.RemoteAddr, ":")[0])

	// forward request
	backendResponse, err := client.Do(forwardReq)
	if err != nil {
		return nil, &ErrorMessage{Message: "failed to complete backend cloudpool request", Detail: err.Error()}
	}

	return backendResponse, nil
}

// ErrorWithCause creates an ErrorMessage from an existing `cause` error.
// In case the `cause` is of type `ErrorMessage`, the given `message` will
// be prepended to the `Message` field of the `cause`. For example, if
// the original `cause` is
//
//    {"message": "x went wrong", "detail": "details"}
//
// a call to
//
//    ErrorWithCause(cause, "failed to do %s", "database lookup")
//
// would result in a new ErrorMessage:
//
//    {"message": "failed to do y: x went wrong", "detail": "details"}
//
// For other types of error causes, an ErrorMessage will be created
// with the following appearance:
//
//    {"message": "<message>", "detail": "cause.Error()"}
//
func ErrorWithCause(cause error, message string, fmtArgs ...interface{}) *ErrorMessage {
	// if the cause is of type ErrorMessage, prepend message to its Message
	// and preserve Detail.
	if causeErr, ok := cause.(*ErrorMessage); ok {
		errMsg := fmt.Sprintf(message+": "+causeErr.Message, fmtArgs...)
		return &ErrorMessage{Message: errMsg, Detail: causeErr.Detail}
	}

	// for generic errors: use message as-is and set the cause error string as detail
	return &ErrorMessage{Message: message, Detail: cause.Error()}
}

// poolURL returns the full URL given a desired host path. For example,
// given a server path of "/a/b/c", the return value could be somehting
// like "http://host:1234/a/b/c"
func (c *DefaultCloudPoolClient) poolURL(resourcePath string) string {
	fullURL := fmt.Sprintf("%s%s", c.CloudPoolURL, path.Clean("/"+resourcePath))
	return fullURL
}
