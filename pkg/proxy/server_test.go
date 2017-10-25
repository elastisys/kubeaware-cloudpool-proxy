package proxy

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
)

type FakeCloudPoolProxy struct {
	ForwardHandler          func(request *http.Request) (*http.Response, error)
	SetDesiredSizeHandler   func(desiredSize int) error
	TerminateMachineHandler func(machineID string, decrementDesiredSize bool) error
}

func (f *FakeCloudPoolProxy) Forward(request *http.Request) (*http.Response, error) {
	return f.ForwardHandler(request)
}

func (f *FakeCloudPoolProxy) SetDesiredSize(desiredSize int) error {
	return f.SetDesiredSizeHandler(desiredSize)
}

func (f *FakeCloudPoolProxy) TerminateMachine(machineID string, decrementDesiredSize bool) error {
	return f.TerminateMachineHandler(machineID, decrementDesiredSize)
}

func doJSONRequest(req *http.Request) (*http.Response, error) {
	client := &http.Client{}
	req.Header.Add("Content-Type", "application/json")
	return client.Do(req)
}

// The server should forward all requests (except `POST /pool/terminate/`
// and `POST /pool/size`) should simply be forwarded to CloudPoolProxy.Forward().
// This test verifies that the server just passes requests on (unchanged)
// responds with an (unaltered) response to the client.
func TestRequestForwarding(t *testing.T) {
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	tests := []struct {
		method             string
		path               string
		body               string
		fakeResponseStatus int
		fakeResponseBody   string
	}{
		{method: "GET", path: "/config", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/config", body: `{"a": 1}`, fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/start", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/stop", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "GET", path: "/status", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "GET", path: "/pool", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "GET", path: "/pool/size", body: "", fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/pool/detach", body: `{"machineId": "i-123", "decrementDesiredSize": true}`, fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/pool/attach", body: `{"machineId": "i-123"}`, fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/pool/serviceState", body: `{"machineId": "i-123", "serviceState": "IN_SERVICE"}`, fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
		{method: "POST", path: "/pool/membershipStatus", body: `{"machineId": "i-123", "membershipStatus": {"active": false, "evictable": false}}`, fakeResponseStatus: 200, fakeResponseBody: `{"ok": true}`},
	}

	for _, test := range tests {

		var calledWithPath string
		var calledWithMethod string
		var calledWithBody string

		// a fake CloudPoolProxy.Forward method that records the request it
		// was called with and responds according to the fake response it has
		// that the test has been set up to use
		forwardHandler := func(request *http.Request) (*http.Response, error) {
			defer request.Body.Close()
			bodyBytes, _ := ioutil.ReadAll(request.Body)
			calledWithBody = string(bodyBytes)
			calledWithMethod = request.Method
			calledWithPath = request.RequestURI

			response := &http.Response{
				StatusCode: test.fakeResponseStatus,
				Body:       ioutil.NopCloser(strings.NewReader(test.fakeResponseBody)),
			}
			return response, nil
		}
		fakeCloudPoolProxy.ForwardHandler = forwardHandler

		req, _ := http.NewRequest(test.method, httpServer.URL+test.path,
			ioutil.NopCloser(strings.NewReader(test.body)))
		resp, err := doJSONRequest(req)
		defer resp.Body.Close()
		require.Nil(t, err, "server unexpectedly failed to forward request")
		assert.Equal(t, test.method, calledWithMethod, "server forwarded request with unexpected method")
		assert.Equal(t, test.path, calledWithPath, "server forwarded request with unexpected path")
		assert.Equal(t, test.body, calledWithBody, "server forwarded request with unexpected body")
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		assert.Equal(t, test.fakeResponseBody, string(bodyBytes), "server responded with unexpected body")
	}
}

// Ensure that `POST /pool/terminate/` requests are properly
// unmarshalled and forwarded to 'CloudPoolProxy.TerminateMachine()`
// and that the CloudPoolProxy return value is properly converted to
// an HTTP response.
func TestTerminateMachineForwarding(t *testing.T) {
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	tests := []struct {
		name                               string
		method                             string
		path                               string
		body                               string
		fakeReturn                         error
		expectedFakeParamMachineID         string
		expectedFakeParamDecrementPoolSize bool
		expectedServerResponseCode         int
	}{
		{
			name:                               "terminate with machineId and decrement=true",
			method:                             "POST",
			path:                               "/pool/terminate",
			body:                               `{"machineId": "i-123", "decrementDesiredSize": true}`,
			fakeReturn:                         nil,
			expectedFakeParamMachineID:         "i-123",
			expectedFakeParamDecrementPoolSize: true,
			expectedServerResponseCode:         200,
		},
		{
			name:                               "terminate with machineId and decrement=false",
			method:                             "POST",
			path:                               "/pool/terminate",
			body:                               `{"machineId": "i-123", "decrementDesiredSize": false}`,
			fakeReturn:                         nil,
			expectedFakeParamMachineID:         "i-123",
			expectedFakeParamDecrementPoolSize: false,
			expectedServerResponseCode:         200,
		},
		{
			name:                               "terminate on CloudPoolProxy.Terminate() error return",
			method:                             "POST",
			path:                               "/pool/terminate",
			body:                               `{"machineId": "i-123", "decrementDesiredSize": true}`,
			fakeReturn:                         fmt.Errorf("some error"),
			expectedFakeParamMachineID:         "i-123",
			expectedFakeParamDecrementPoolSize: true,
			expectedServerResponseCode:         500,
		},
	}

	for _, test := range tests {

		var calledWithMachineID string
		var calledWithDecrementDesiredSize bool

		// a fake CloudPoolProxy.Forward method that records the request it
		// was called with and responds according to the fake response it has
		// that the test has been set up to use
		terminateHandler := func(machineID string, decrementDesiredSize bool) error {
			calledWithMachineID = machineID
			calledWithDecrementDesiredSize = decrementDesiredSize

			return test.fakeReturn
		}
		fakeCloudPoolProxy.TerminateMachineHandler = terminateHandler

		req, _ := http.NewRequest(test.method, httpServer.URL+test.path,
			ioutil.NopCloser(strings.NewReader(test.body)))
		resp, err := doJSONRequest(req)
		require.Nil(t, err, "[%s]: server unexpectedly failed to forward request", test.name)

		// verify that HTTP request was properly unmarshalled into call arguments
		require.Equal(t,
			test.expectedFakeParamMachineID,
			calledWithMachineID,
			"[%s]: server forwarded terminate request with wrong machineID", test.name)
		require.Equal(t,
			test.expectedFakeParamDecrementPoolSize,
			calledWithDecrementDesiredSize,
			"[%s]: server forwarded terminate request with wrong decrementDesiredSize", test.name)

		// verify proper response status code
		require.Equal(t,
			test.expectedServerResponseCode,
			resp.StatusCode,
			"[%s]: server responded with unexpected status code", test.name)
		// verify proper response body
		if test.fakeReturn != nil {
			defer resp.Body.Close()
			bodyBytes, _ := ioutil.ReadAll(resp.Body)
			require.Equal(t,
				fmt.Sprintf("{\n  \"message\": \"failed to terminate machine\",\n  \"detail\": \"%s\"\n}", test.fakeReturn),
				string(bodyBytes),
				"[%s]: server responded with unexpected body", test.name)
		}
	}
}

// The server must parse the incoming TerminateMachine request to ensure
// a valid request before passing the request on to the CloudPoolProxy.
// Should this unmarshalling fail, a 400 (Bad Request) response must be
// produced.
func TestTerminateMachineOnBadRequest(t *testing.T) {
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	tests := []struct {
		name                       string
		method                     string
		path                       string
		body                       string
		expectedServerResponseCode int
	}{
		// on illegal JSON syntax in request body => 400 (Bad Request)
		{
			name:   "terminate on illegal JSON syntax in request",
			method: "POST",
			path:   "/pool/terminate",
			body:   `"machineId": "i-123", "decrementDesiredSize": true`,
			expectedServerResponseCode: 400,
		},
		// on malformed request body (missing "machineId") => 400 (Bad Request)
		{
			name:   "terminate on missing machineId",
			method: "POST",
			path:   "/pool/terminate",
			body:   `{"decrementDesiredSize": true}`,
			expectedServerResponseCode: 400,
		},
	}

	for _, test := range tests {

		req, _ := http.NewRequest(test.method, httpServer.URL+test.path,
			ioutil.NopCloser(strings.NewReader(test.body)))
		resp, err := doJSONRequest(req)
		defer resp.Body.Close()
		require.Nil(t, err, "[%s]: server unexpectedly failed to forward request", test.name)
		require.Equal(t,
			test.expectedServerResponseCode,
			resp.StatusCode,
			"[%s]: server responded with unexpected status code", test.name)
	}
}

// Ensure that `POST /pool/size` requests are properly
// unmarshalled and forwarded to 'CloudPoolProxy.SetDesiredSize()`.
func TestSetDesiredSizeForwarding(t *testing.T) {
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	tests := []struct {
		name                         string
		method                       string
		path                         string
		body                         string
		fakeReturn                   error
		expectedFakeParamDesiredSize int
		expectedServerResponseCode   int
	}{
		{
			name:                         "setDesiredSize with desiredSize=1",
			method:                       "POST",
			path:                         "/pool/size",
			body:                         `{"desiredSize": 1}`,
			fakeReturn:                   nil,
			expectedFakeParamDesiredSize: 1,
			expectedServerResponseCode:   200,
		},
		{
			name:                         "setDesiredSize with desiredSize=3",
			method:                       "POST",
			path:                         "/pool/size",
			body:                         `{"desiredSize": 3}`,
			fakeReturn:                   nil,
			expectedFakeParamDesiredSize: 3,
			expectedServerResponseCode:   200,
		},
		{
			name:                         "setDesiredSize on CloudPoolProxy.SetDesiredSize() error return",
			method:                       "POST",
			path:                         "/pool/size",
			body:                         `{"desiredSize": 3}`,
			fakeReturn:                   fmt.Errorf("some error"),
			expectedFakeParamDesiredSize: 3,
			expectedServerResponseCode:   500,
		},
	}

	for _, test := range tests {

		var calledWithDesiredSize int

		// a fake CloudPoolProxy.SetDesiredSize method that records the request it
		// was called with and responds according to the fake response it has
		// that the test has been set up to use
		setDesiredSizeHandler := func(desiredSize int) error {
			calledWithDesiredSize = desiredSize
			return test.fakeReturn
		}
		fakeCloudPoolProxy.SetDesiredSizeHandler = setDesiredSizeHandler

		req, _ := http.NewRequest(test.method, httpServer.URL+test.path,
			ioutil.NopCloser(strings.NewReader(test.body)))
		resp, err := doJSONRequest(req)
		require.Nil(t, err, "[%s]: server unexpectedly failed to forward request", test.name)

		// verify that HTTP request was properly unmarshalled into call arguments
		require.Equal(t,
			test.expectedFakeParamDesiredSize,
			calledWithDesiredSize,
			"[%s]: server forwarded setDesiredSize request with wrong desiredSize", test.name)
		// verify proper response status code
		require.Equal(t,
			test.expectedServerResponseCode,
			resp.StatusCode,
			"[%s]: server responded with unexpected status code", test.name)
		// verify proper response body
		if test.fakeReturn != nil {
			defer resp.Body.Close()
			bodyBytes, _ := ioutil.ReadAll(resp.Body)
			require.Equal(t,
				fmt.Sprintf("{\n  \"message\": \"failed to set desired size\",\n  \"detail\": \"%s\"\n}", test.fakeReturn),
				string(bodyBytes),
				"[%s]: server responded with unexpected body", test.name)
		}
	}
}

// The server must parse the incoming SetDesiredSize request to ensure
// a valid request before passing the request on to the CloudPoolProxy.
// Should this unmarshalling fail, a 400 (Bad Request) response must be
// produced.
func TestSetDesiredSizeOnBadRequest(t *testing.T) {
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	tests := []struct {
		name                       string
		method                     string
		path                       string
		body                       string
		expectedServerResponseCode int
	}{
		// on illegal JSON syntax in request body => 400 (Bad Request)
		{
			name:   "setDesiredSize on illegal JSON syntax in request",
			method: "POST",
			path:   "/pool/size",
			body:   `"desiredSize": 2`,
			expectedServerResponseCode: 400,
		},
		// on illegal desiredSize => 400 (Bad Request)
		{
			name:   "setDesiredSize on missing machineId",
			method: "POST",
			path:   "/pool/size",
			body:   `{"desiredSize": -3}`,
			expectedServerResponseCode: 400,
		},
	}

	for _, test := range tests {
		req, _ := http.NewRequest(test.method, httpServer.URL+test.path,
			ioutil.NopCloser(strings.NewReader(test.body)))
		resp, err := doJSONRequest(req)
		defer resp.Body.Close()
		require.Nil(t, err, "[%s]: server unexpectedly failed to forward request", test.name)
		require.Equal(t,
			test.expectedServerResponseCode,
			resp.StatusCode,
			"[%s]: server responded with unexpected status code", test.name)
	}
}

type TimePeriod struct {
	Start time.Time
	End   time.Time
}

func (p TimePeriod) String() string {
	return fmt.Sprintf("[%s, %s]", p.Start, p.End)
}

func (p TimePeriod) HappenedBefore(p2 TimePeriod) bool {
	return p.Start.Before(p2.Start) && p.End.Before(p2.Start)
}

// CloudPoolProxy operations that may update the backend pool and Kubernetes
// cluster (`SetDesiredSize()` and `TerminateMachine()`) are to be executed
// one-at-a-time to prevent updates from interfering with each other.
func TestNoConcurrentUpdatesAllowed(t *testing.T) {
	// Run a test that launches a number of updates concurrently,
	// record the start and end time of each operation in the CloudPoolProxy,
	// and ensure that no operations overlap in time.
	fakeCloudPoolProxy := &FakeCloudPoolProxy{}
	proxyServer := NewServer(fakeCloudPoolProxy, 8080, 0)
	httpServer := httptest.NewServer(proxyServer.httpServer.Handler)
	defer httpServer.Close()

	maxOpDelayMillis := 50
	timings := make(chan TimePeriod)

	// a fake CloudPoolProxy.Forward method that records the time at which it
	// was called and returns that on the timings channel
	terminateHandler := func(machineID string, decrementDesiredSize bool) error {
		timing := TimePeriod{Start: time.Now().UTC()}
		// simulate some processing time
		delay := time.Duration(rand.Int() % maxOpDelayMillis)
		time.Sleep(delay * time.Millisecond)

		timing.End = time.Now().UTC()
		timings <- timing
		return nil
	}
	fakeCloudPoolProxy.TerminateMachineHandler = terminateHandler

	// a fake CloudPoolProxy.Forward method that records the time at which it
	// was called and returns that on the timings channel
	setDesiredSizeHandler := func(desiredSize int) error {
		timing := TimePeriod{Start: time.Now().UTC()}
		// simulate some processing time
		delay := time.Duration(rand.Int() % maxOpDelayMillis)
		time.Sleep(delay * time.Millisecond)

		timing.End = time.Now().UTC()
		timings <- timing
		return nil
	}
	fakeCloudPoolProxy.SetDesiredSizeHandler = setDesiredSizeHandler

	// run a bunch of concurrent updates
	for i := 0; i < 10; i++ {
		// setDesiredSize
		go func() {
			req, _ := http.NewRequest("POST", httpServer.URL+"/pool/size",
				ioutil.NopCloser(strings.NewReader(`{"desiredSize": 1}`)))
			resp, err := doJSONRequest(req)
			assert.Nil(t, err, "setDesiredSize unexpectedly failed")
			defer resp.Body.Close()
		}()

		// terminateMachine
		go func() {
			req, _ := http.NewRequest("POST", httpServer.URL+"/pool/terminate",
				ioutil.NopCloser(strings.NewReader(`{"machineID": "i-123", "decrementDesiredSize": true}`)))
			resp, err := doJSONRequest(req)
			assert.Nil(t, err, "terminate unexpectedly failed")
			defer resp.Body.Close()
		}()
	}

	// collect timings
	var updateTimings []TimePeriod
	for i := 0; i < 20; i++ {
		updateTimings = append(updateTimings, <-timings)
	}

	// ensure no timings overlap (that is, updates were processed one-by-one)
	sort.Slice(updateTimings, func(i int, j int) bool {
		return updateTimings[i].Start.Before(updateTimings[j].Start)
	})
	for i := 0; i < len(updateTimings)-1; i++ {
		assert.True(t, updateTimings[i].HappenedBefore(updateTimings[i+1]),
			"updates overlap in time: %s and %s", updateTimings[i], updateTimings[i+1])
	}
}
