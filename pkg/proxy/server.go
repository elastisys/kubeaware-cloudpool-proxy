package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/golang/glog"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/config"
	"github.com/gorilla/mux"
)

// Server is a proxy server that forwards all requests to a backend cloudpool,
// except for operations that affect the size of the cloudpool. In particular,
// scale-downs need to be gracefully handled to incur as little disruption as
// possible to the Kubernetes cluster. Any such requests are therefore forwarded
// to a CloudPoolProxy.
type Server struct {
	// proxy is the CloudPoolProxy that handles scale-down requests and
	// forwards other requests to the backend cloudpool.
	proxy CloudPoolProxy
	// httpServer is the HTTP(S) server that accepts and dispatches
	// client requests.
	httpServer *http.Server
	// A lock to prevent concurrent updates to the  the cloudpool/Kubernetes
	// cluster.
	updateLock sync.Mutex
}

// NewServer creates a new cloudpool proxy server, listening on the given port.
// Note that this does not start the server. For that, call
//
//     server.ListenAndServe()
//
// If `readTimeout` is 0 or negative, a default value will be used.
//
func NewServer(proxy CloudPoolProxy, port int, readTimeout time.Duration) *Server {
	server := Server{proxy: proxy}

	// set up request dispatching logic
	router := mux.NewRouter()
	// setDesiredSize requires special treatment
	router.HandleFunc("/pool/size", server.HandleSetDesiredSize).Methods("POST")
	// terminateMachine requires special treatment
	router.HandleFunc("/pool/terminate", server.HandleTerminateMachine).Methods("POST")
	// all other routes are to be forwarded immediately to the backend
	router.PathPrefix("/").HandlerFunc(server.ForwardToBackend)

	if readTimeout <= 0 {
		readTimeout = config.DefaultServerTimeout
	}

	server.httpServer = &http.Server{
		Addr:        fmt.Sprintf(":%d", port),
		Handler:     router,
		ReadTimeout: readTimeout,
	}

	return &server
}

// ListenAndServe starts the HTTP(S) server on the given port
// and starts accepting client connections.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// HandleSetDesiredSize handles a `POST /pool/size` request by passing it to the proxy.
func (s *Server) HandleSetDesiredSize(w http.ResponseWriter, r *http.Request) {
	glog.V(0).Infof("handling %s %s called by %s", r.Method, r.RequestURI, r.RemoteAddr)
	bytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest,
			cloudpool.ErrorWithCause(err, "failed to read request body"))
		return
	}
	defer r.Body.Close()

	setDesiredSizeMsg := &cloudpool.SetDesiredSizeMessage{}
	err = json.Unmarshal(bytes, setDesiredSizeMsg)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest,
			cloudpool.ErrorWithCause(err, "failed to decode setDesiredSize message"))
		return
	}
	err = setDesiredSizeMsg.Validate()
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, &cloudpool.ErrorMessage{Message: err.Error()})
		return
	}

	// ensure concurrent updates are not allowed
	s.updateLock.Lock()
	err = s.proxy.SetDesiredSize(*setDesiredSizeMsg.DesiredSize)
	s.updateLock.Unlock()

	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError,
			cloudpool.ErrorWithCause(err, "failed to set desired size"))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// HandleTerminateMachine handles a `POST /pool/terminate` request by passing it to the proxy.
func (s *Server) HandleTerminateMachine(w http.ResponseWriter, r *http.Request) {
	glog.V(0).Infof("handling %s %s called by %s", r.Method, r.RequestURI, r.RemoteAddr)
	bytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest,
			cloudpool.ErrorWithCause(err, "failed to read request body"))
		return
	}
	defer r.Body.Close()

	terminateMachineMsg := &cloudpool.TerminateMachineMessage{}
	err = json.Unmarshal(bytes, terminateMachineMsg)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest,
			cloudpool.ErrorWithCause(err, "failed to decode terminateMachine message"))
		return
	}
	err = terminateMachineMsg.Validate()
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, &cloudpool.ErrorMessage{Message: err.Error()})
		return
	}

	s.updateLock.Lock()
	err = s.proxy.TerminateMachine(terminateMachineMsg.MachineID, terminateMachineMsg.DecrementDesiredSize)
	s.updateLock.Unlock()

	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError,
			cloudpool.ErrorWithCause(err, "failed to terminate machine"))
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ForwardToBackend forwards the incoming request to the backend cloudpool and
// responds to the client with response retrieved from the backend cloudpool.
func (s *Server) ForwardToBackend(w http.ResponseWriter, r *http.Request) {
	glog.V(0).Infof("forwarding %s %s from %s to backend", r.Method, r.RequestURI, r.RemoteAddr)
	backendResponse, err := s.proxy.Forward(r)
	if err != nil {
		writeErrorResponse(w, http.StatusBadGateway,
			cloudpool.ErrorWithCause(err, "failed to forward request to backend"))
		return
	}
	defer backendResponse.Body.Close()

	// pass a copy of the backend response to the client
	responseHeaders := w.Header()
	// copy response headers
	for headerName, headerValues := range backendResponse.Header {
		for _, headerValue := range headerValues {
			responseHeaders.Add(headerName, headerValue)
		}
	}
	// copy response code
	w.WriteHeader(backendResponse.StatusCode)
	// copy response body
	_, err = io.Copy(w, backendResponse.Body)
	if err != nil {
		writeErrorResponse(w, http.StatusBadGateway,
			cloudpool.ErrorWithCause(err, "failed to read backend response body"))
		return
	}
}

// writeErrorResponse writes an error response with a given HTTP status code
// and JSON ErrorMessage.
func writeErrorResponse(w http.ResponseWriter, statusCode int, errMsg *cloudpool.ErrorMessage) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	bytes, err := json.MarshalIndent(errMsg, "", "  ")
	if err != nil {
		glog.Errorf("failed to marshal error message: %s", err)
	}

	if bytes != nil {
		w.Write(bytes)
	}
}
