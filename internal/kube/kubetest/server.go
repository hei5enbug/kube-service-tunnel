// Package kubetest provides a hermetic Kubernetes fixture for tests: a fake API server on 127.0.0.1 that
// serves namespaces, services and pods, streams pod watch events, answers SPDY port-forward requests, and
// writes kubeconfig files that point at it.
//
// Every Server is created per test and shut down with t.Cleanup; the package holds no global state, so tests
// using it can run in parallel.
package kubetest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/httpstream"
)

// defaultWaitTimeout bounds every Wait helper; it is an upper limit for slow machines, not an expected duration.
const defaultWaitTimeout = 15 * time.Second

// initialVersion is the resourceVersion counter's starting value; objects written later get higher values.
const initialVersion = 100

// Config describes what a new Server serves. The zero value serves nothing. The Server keeps its own copies,
// so later changes to the maps do not affect it.
type Config struct {
	// Namespaces are listed in order by GET /api/v1/namespaces.
	Namespaces []string
	// Services and Pods are keyed by namespace.
	Services map[string][]corev1.Service
	Pods     map[string][]corev1.Pod
	// Failures maps an exact request path to the HTTP status answered instead of the normal response.
	Failures map[string]int
}

// Server is a fake Kubernetes API server. Its methods are safe for concurrent use.
type Server struct {
	tb   testing.TB
	http *httptest.Server

	mu         sync.Mutex
	closed     bool
	namespaces []string
	services   map[string][]corev1.Service
	pods       map[string][]corev1.Pod
	failures   map[string]int
	requests   []string
	version    int64
	uids       int
	// waitTimeout is how long the Wait helpers block before they fail the test.
	waitTimeout time.Duration

	watches       map[*watchStream]struct{}
	watchRequests int
	watchFailures []int
	watchSignal   signal

	backends    map[string]string
	conns       []httpstream.Connection
	connsSignal signal
}

// NewServer starts a fake API server and registers its shutdown with tb.Cleanup.
func NewServer(tb testing.TB, config Config) *Server {
	tb.Helper()
	s := &Server{
		tb:          tb,
		namespaces:  append([]string(nil), config.Namespaces...),
		services:    copyServices(config.Services),
		pods:        copyPods(config.Pods),
		failures:    copyFailures(config.Failures),
		version:     initialVersion,
		waitTimeout: defaultWaitTimeout,
		watches:     make(map[*watchStream]struct{}),
		backends:    make(map[string]string),
		watchSignal: newSignal(),
		connsSignal: newSignal(),
	}
	s.http = httptest.NewServer(s)
	tb.Cleanup(s.shutdown)
	return s
}

// URL returns the base URL of the server, for example http://127.0.0.1:34567.
func (s *Server) URL() string { return s.http.URL }

// Requests returns every request received so far as "path?query", in arrival order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// RequestsTo returns the recorded "path?query" entries whose path ends with suffix.
func (s *Server) RequestsTo(suffix string) []string {
	var matched []string
	for _, request := range s.Requests() {
		if strings.HasSuffix(strings.SplitN(request, "?", 2)[0], suffix) {
			matched = append(matched, request)
		}
	}
	return matched
}

// RequestsWith returns the entries of RequestsTo(suffix) that contain fragment.
func (s *Server) RequestsWith(suffix, fragment string) []string {
	var matched []string
	for _, request := range s.RequestsTo(suffix) {
		if strings.Contains(request, fragment) {
			matched = append(matched, request)
		}
	}
	return matched
}

// FailPath makes every later request to the exact path answer with the HTTP status.
func (s *Server) FailPath(path string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[path] = status
}

// RestorePath removes a failure set with Config.Failures or FailPath.
func (s *Server) RestorePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, path)
}

// ServeHTTP implements http.Handler; it is exported only because the server hands itself to httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	if status, failing := s.failure(r.URL.Path); failing {
		writeStatus(w, status)
		return
	}

	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	isWatch := r.URL.Query().Get("watch") == "true"
	switch {
	case matchPath(segments, "api", "v1", "namespaces"):
		writeJSON(w, s.namespaceList())
	case matchPath(segments, "api", "v1", "namespaces", "*", "services"):
		writeJSON(w, s.serviceList(segments[3]))
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods") && isWatch:
		s.serveWatch(w, r, segments[3])
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods"):
		writeJSON(w, s.podList(segments[3], r.URL.Query().Get("labelSelector")))
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods", "*"):
		s.servePod(w, segments[3], segments[5])
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods", "*", "portforward"):
		s.servePortForward(w, r, segments[3], segments[5])
	default:
		writeStatus(w, http.StatusNotFound)
	}
}

func (s *Server) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.URL.Path+"?"+r.URL.RawQuery)
}

func (s *Server) failure(path string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, failing := s.failures[path]
	return status, failing
}

func (s *Server) namespaceList() corev1.NamespaceList {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := corev1.NamespaceList{TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"}}
	for _, name := range s.namespaces {
		list.Items = append(list.Items, corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	return list
}

func (s *Server) serviceList(namespace string) corev1.ServiceList {
	s.mu.Lock()
	defer s.mu.Unlock()
	return corev1.ServiceList{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceList", APIVersion: "v1"},
		Items:    append([]corev1.Service(nil), s.services[namespace]...),
	}
}

func (s *Server) podList(namespace, selector string) corev1.PodList {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}}
	parsed, err := labels.Parse(selector)
	if err != nil {
		return list
	}
	for _, pod := range s.pods[namespace] {
		if parsed.Matches(labels.Set(pod.Labels)) {
			list.Items = append(list.Items, *pod.DeepCopy())
		}
	}
	return list
}

func (s *Server) servePod(w http.ResponseWriter, namespace, name string) {
	pod, found := s.GetPod(namespace, name)
	if !found {
		writeStatus(w, http.StatusNotFound)
		return
	}
	pod.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}
	writeJSON(w, pod)
}

// shutdown ends every long-lived exchange first, because httptest.Server.Close waits for active requests.
func (s *Server) shutdown() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.CloseWatches()
	s.DropForwardConnections()
	s.http.CloseClientConnections()
	s.http.Close()
}

func matchPath(segments []string, pattern ...string) bool {
	if len(segments) != len(pattern) {
		return false
	}
	for i, want := range pattern {
		if want != "*" && segments[i] != want {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeStatus(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Message:  http.StatusText(code),
		Code:     int32(code),
	})
}

func copyServices(in map[string][]corev1.Service) map[string][]corev1.Service {
	out := make(map[string][]corev1.Service, len(in))
	for namespace, services := range in {
		for _, service := range services {
			out[namespace] = append(out[namespace], *service.DeepCopy())
		}
	}
	return out
}

func copyPods(in map[string][]corev1.Pod) map[string][]corev1.Pod {
	out := make(map[string][]corev1.Pod, len(in))
	for namespace, pods := range in {
		for _, pod := range pods {
			out[namespace] = append(out[namespace], *pod.DeepCopy())
		}
	}
	return out
}

func copyFailures(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for path, status := range in {
		out[path] = status
	}
	return out
}
