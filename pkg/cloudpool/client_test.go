package cloudpool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
)

type fakeCloudPoolRequest struct {
	Method      string
	RequestPath string
	Body        string
	Headers     map[string][]string
}

type fakeCloudPoolResponse struct {
	StatusCode int
	Body       string
	Headers    map[string][]string
}

// fakeCloudPool represents a fake cloudpool backend REST API endpoint that
// records interactions and can be prepared with responses.
type fakeCloudPool struct {
	// NextResponse holds the response that the fake server will produce when it receives next request.
	NextResponse *fakeCloudPoolResponse
	// Requests tracks observed request in chronological order.
	Requests []*fakeCloudPoolRequest
}

// Handle takes care of a single incoming request for a fakeCloudPoolServer.
// It responds with the `NextResponse` it's been set up to use.
func (f *fakeCloudPool) Handle(w http.ResponseWriter, r *http.Request) {
	f.Requests = append(f.Requests, f.copyRequest(r))

	for headerKey, headerValues := range f.NextResponse.Headers {
		for _, headerValue := range headerValues {
			w.Header().Set(headerKey, headerValue)
		}
	}
	w.WriteHeader(f.NextResponse.StatusCode)
	fmt.Fprintf(w, f.NextResponse.Body)
}

func (f *fakeCloudPool) copyRequest(r *http.Request) *fakeCloudPoolRequest {
	bodyBytes, _ := ioutil.ReadAll(r.Body)
	return &fakeCloudPoolRequest{Method: r.Method, RequestPath: r.URL.Path, Body: string(bodyBytes), Headers: r.Header}
}

func createRequest(method string, remoteURL string, body string, clientAddr string) *http.Request {
	req, _ := http.NewRequest(method, remoteURL, bytes.NewReader([]byte(body)))
	req.RemoteAddr = clientAddr
	req.Header.Add("Content-Type", "application/json")
	return req
}

// Not specifying a timeout should result in a default timeout being set.
func TestCreateClientWithDefaultTimeout(t *testing.T) {
	client := NewClient("https://cloudpool:443", 0)
	assert.Equal(t, config.DefaultBackendTimeout, client.Timeout, "wrong default timeout set")
}

// ensure that DefaultCloudPoolClient.Forward forwards a given request (with right content)
// to the backend cloudpool and returns an unaltered response from the backend
func TestForward(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       `{"status": "ok"}`,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	// forward a request to the cloudpool via DefaultCloudPoolClient
	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}

	clientIP := "1.2.3.4"
	clientAddr := fmt.Sprintf("%s:12345", clientIP)
	reqBody := `{"key": "value"}`
	clientReq := createRequest("GET", "/config", reqBody, clientAddr)
	clientReq.Header.Add("X-My-Custom-Header", "My-Custom-Value")
	resp, err := poolClient.Forward(clientReq)
	assert.Nil(t, err, "unexpectedly failed to forward request to backend: %s", err)

	//
	// verify that a request with the right content was forwarded by the client to the backend cloudpool
	//
	handledRequests := len(fakeBackend.Requests)
	assert.Equal(t, 1, handledRequests, "unexpected number of handled fake requests")
	assert.Equal(t, reqBody, fakeBackend.Requests[0].Body, "unexpected request body forwarded by client")
	backendObservedReq := fakeBackend.Requests[0]
	// verify that all client-side request headers were passed on to backend
	if !containsHeaders(backendObservedReq.Headers, clientReq.Header) {
		t.Errorf("not all client headers were passed on to backend: backend got: %s, expected: %s", backendObservedReq.Headers, clientReq.Header)
	}
	// verify that client IP (request remoteAddr) was passed on as X-Forwarded-For header
	if headerValues, exists := backendObservedReq.Headers["X-Forwarded-For"]; !exists || headerValues[0] != clientIP {
		t.Errorf("no/incorrect X-Forwarded-For header passed to backend")
	}

	//
	// verify that the expected cloudpool response is unaltered
	//
	assert.Equal(t, expectedResponse.StatusCode, resp.StatusCode, "client response status code differs from backend response")
	bodyBytes, _ := ioutil.ReadAll(resp.Body)
	gotBody := string(bodyBytes)
	assert.Equal(t, expectedResponse.Body, gotBody, "client response body differs from backend response")
	if !containsHeaders(resp.Header, expectedResponse.Headers) {
		t.Errorf("not all backend response headers were returned by client: got: %s, expected: %s", resp.Header, expectedResponse.Headers)
	}
	//
	// verify that client request was made on the right server path
	//
	reqMethod := fakeBackend.Requests[0].Method
	expectedMethod := "GET"
	assert.Equal(t, expectedMethod, reqMethod, "client used an unexpected request method")
	expectedPath := "/config"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client used an unexpected request path")
}

// Verify that DefaultCloudPoolClient.GetPoolSize invokes the right operation on the backend
// and correctly unmarshals the response.
func TestGetPoolSize(t *testing.T) {
	// prepare a pool size message to be returned by cloudpool
	expectedPoolSize := &PoolSizeMessage{Active: 1, Allocated: 1, DesiredSize: 1, Timestamp: time.Now().UTC()}
	poolSizeJSON := expectedPoolSize.String()

	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       poolSizeJSON,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	actualPoolSize, _ := poolClient.GetPoolSize()

	//
	// verify response
	//
	assert.Equal(t, expectedPoolSize, actualPoolSize, "client returned wrong response")

	//
	// verify that client request was made on the right server path
	//
	expectedMethod := "GET"
	reqMethod := fakeBackend.Requests[0].Method
	assert.Equal(t, expectedMethod, reqMethod, "client used unexpected request method")

	expectedPath := "/pool/size"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client used unexpected request path")
}

// Verify that DefaultCloudPoolClient.GetMachinePool invokes the right operation on the backend
// and correctly unmarshals the response.
func TestGetMachinePool(t *testing.T) {
	// prepare a pool size message to be returned by cloudpool
	expectedPool := &MachinePoolMessage{
		Timestamp: time.Now().UTC(),
		Machines:  []Machine{*machine("i-1", MachineStatePending), *machine("i-2", MachineStateRunning)},
	}
	poolJSON := expectedPool.String()

	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       poolJSON,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	actualPool, _ := poolClient.GetMachinePool()

	//
	// verify response
	//
	assert.Equal(t, expectedPool, actualPool, "client returned wrong response")

	//
	// verify that client request was made on the right server path
	//
	expectedMethod := "GET"
	reqMethod := fakeBackend.Requests[0].Method
	assert.Equal(t, expectedMethod, reqMethod, "client used wrong request method")
	expectedPath := "/pool"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client used wrong request path")
}

// Verify that an error response results in a proper ErrorMessage return value
func TestGetMachinePoolOnInternalError(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       `{"message": "internal error", "detail": "stacktrace"}`,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 503,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	_, err := poolClient.GetMachinePool()
	assert.NotNil(t, err, "expected to fail")
	// verify proper error message chaining
	assert.IsType(t, &ErrorMessage{}, err, "expected error to be of type ErrorMessage")
	e := err.(*ErrorMessage)
	assert.Equal(t, "getMachinePool: internal error", e.Message, "unexpected error message")
}

// Verify that DefaultCloudPoolClient.SetDesiredSize invokes the right operation on the backend
// and correctly forwards the request.
func TestSetDesiredSize(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       "",
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	expectedDesiredSize := 10
	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	err := poolClient.SetDesiredSize(expectedDesiredSize)
	assert.Nil(t, err, "setDesiredSize call unexpectedly failed")

	//
	// verify that client request was made on the right server path
	//
	expectedMethod := "POST"
	reqMethod := fakeBackend.Requests[0].Method
	assert.Equal(t, expectedMethod, reqMethod, "client did not use expected request method")
	expectedPath := "/pool/size"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client did not use expected request path")
	// verify that the correct message was sent to backend
	var setDesiredSizeMsg SetDesiredSizeMessage
	e := json.Unmarshal([]byte(fakeBackend.Requests[0].Body), &setDesiredSizeMsg)
	assert.Nil(t, e, "client did not send a SetDesiredSizeMessage to backend")
	assert.Equal(t, expectedDesiredSize, *setDesiredSizeMsg.DesiredSize, "client set wrong desired size on backend")
}

// Verify that an error response results in a proper ErrorMessage return value
func TestSetDesiredSizeOnInternalError(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       `{"message": "internal error", "detail": "stacktrace"}`,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 503,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	err := poolClient.SetDesiredSize(1)

	assert.NotNil(t, err, "expected to fail")
	// verify proper error message chaining
	assert.IsType(t, &ErrorMessage{}, err, "expected error to be of type ErrorMessage")
	e := err.(*ErrorMessage)
	assert.Equal(t,
		"setDesiredSize 1: internal error", e.Message, "unexpected error message")
}

// Verify that DefaultCloudPoolClient.TerminateMachine invokes the right operation on the backend
// and correctly forwards the request.
func TestTerminateMachine(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       "",
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	victimMachine := "i-1"
	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	err := poolClient.TerminateMachine(victimMachine, true)
	assert.Nil(t, err, "terminateMachine call unexpectedly failed")

	//
	// verify that client request was made on the right server path
	//
	expectedMethod := "POST"
	reqMethod := fakeBackend.Requests[0].Method
	assert.Equal(t, expectedMethod, reqMethod, "client did not use expected request method")
	expectedPath := "/pool/terminate"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client did not use expected request path")
	// verify that the correct message was sent to backend
	var terminateMsg TerminateMachineMessage
	e := json.Unmarshal([]byte(fakeBackend.Requests[0].Body), &terminateMsg)
	assert.Nil(t, e, "client did not send a TerminateMachineMessage to backend")

	assert.Equal(t, victimMachine, terminateMsg.MachineID, "client sent wrong victim machine to backend")
	assert.True(t, terminateMsg.DecrementDesiredSize, "client did not tell backend to decrement desired size")
}

// Verify correct handling of error response (404 Not Found) from backend cloudpool.
func TestTerminateMachineOnNotFoundError(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedResponse := &fakeCloudPoolResponse{
		Body:       "",
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 404,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	err := poolClient.TerminateMachine("i-X", true)

	assert.NotNil(t, err, "expected terminateMachine to fail")
	// verify proper error message chaining
	assert.IsType(t, &ErrorMessage{}, err, "expected error to be of type ErrorMessage")
	e := err.(*ErrorMessage)
	assert.Equal(t,
		"terminateMachine i-X: backend cloudpool responded with HTTP status: 404 Not Found",
		e.Message, "unexpected error message")
}

// Verify that DefaultCloudPoolClient.GetMachine invokes the right operation on the backend
// and correctly forwards the request.
func TestGetMachine(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedPool := &MachinePoolMessage{
		Timestamp: time.Now().UTC(),
		Machines:  []Machine{*machine("i-1", MachineStateRunning), *machine("i-2", MachineStateRunning)},
	}
	poolJSON := expectedPool.String()
	expectedResponse := &fakeCloudPoolResponse{
		Body:       poolJSON,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	gotMachine, err := poolClient.GetMachine("i-1")
	if err != nil {
		t.Errorf("GetMachine call unexpectedly failed: %s", err)
	}

	expectedMachine := machine("i-1", MachineStateRunning)
	assert.Equal(t, expectedMachine, gotMachine, "client did not return correct machine")

	//
	// verify that client request was made on the right server path
	//
	expectedMethod := "GET"
	reqMethod := fakeBackend.Requests[0].Method
	assert.Equal(t, expectedMethod, reqMethod, "client did not use expected request method")
	expectedPath := "/pool"
	reqPath := fakeBackend.Requests[0].RequestPath
	assert.Equal(t, expectedPath, reqPath, "client did not use expected request path")
}

// Verify proper behavior when the requested machine cannot be found in the backend cloudpool.
func TestGetMachineOnNotFoundError(t *testing.T) {
	// set up a fake cloudpool server and prepare its next response
	expectedPool := &MachinePoolMessage{
		Timestamp: time.Now().UTC(),
		Machines:  []Machine{*machine("i-1", MachineStateRunning), *machine("i-2", MachineStateRunning)},
	}
	poolJSON := expectedPool.String()
	expectedResponse := &fakeCloudPoolResponse{
		Body:       poolJSON,
		Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
		StatusCode: 200,
	}
	fakeBackend := fakeCloudPool{NextResponse: expectedResponse}
	server := httptest.NewServer(http.HandlerFunc(fakeBackend.Handle))
	defer server.Close()

	poolClient := &DefaultCloudPoolClient{CloudPoolURL: server.URL, Timeout: 10 * time.Second}
	_, err := poolClient.GetMachine("i-X")

	assert.NotNil(t, err, "expected getMachine to fail")
	// verify proper error message chaining
	assert.IsType(t, &ErrorMessage{}, err, "expected error to be of type ErrorMessage")
	e := err.(*ErrorMessage)
	assert.Equal(t, "getMachine i-X: machine not found", e.Message, "unexpected error message")
}

// Exercise the scenario where a wrong cloudpool URL has been entered.
// Should result in a proper error message.
func TestConnectionToWrongURL(t *testing.T) {
	poolClient := &DefaultCloudPoolClient{CloudPoolURL: "http://unknown-cloudpool:12345", Timeout: 1 * time.Second}
	_, err := poolClient.GetPoolSize()
	if err == nil {
		t.Errorf("expected getMachine to fail")
	}

	// verify proper error message chaining
	assert.IsType(t, &ErrorMessage{}, err, "expected error to be of type ErrorMessage")
	e := err.(*ErrorMessage)
	assert.Equal(t,
		"getPoolSize: backend cloudpool request failed: failed to complete backend cloudpool request",
		e.Message, "unexpected error message")
	assert.True(t, strings.Contains(e.Detail, "no such host"), "unexpected error detail")
}

func containsHeaders(actualHeaders map[string][]string, wantedHeaders map[string][]string) bool {
	for wantedHeaderKey, wantedHeaderValues := range wantedHeaders {
		actualHeaderValues, exists := actualHeaders[wantedHeaderKey]
		if !exists {
			fmt.Errorf("does not include header: %s", wantedHeaderKey)
			return false
		}

		for _, wantedHeaderValue := range wantedHeaderValues {
			if !contains(actualHeaderValues, wantedHeaderValue) {
				fmt.Errorf("does not include value: %s", wantedHeaderValue)
				return false
			}
		}
	}

	return true
}

func contains(strList []string, item string) bool {
	for _, str := range strList {
		if str == item {
			return true
		}
	}
	return false
}
