package kube

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
)

// apiFixture describes the objects a fake Kubernetes API server serves.
type apiFixture struct {
	namespaces []string
	services   map[string][]corev1.Service
	pods       map[string][]corev1.Pod
	// failures maps a request path to the HTTP status the server answers with instead of data.
	failures map[string]int
	// forward handles pod port-forward requests; nil answers 404.
	forward http.Handler
}

// fakeAPI is a running fake Kubernetes API server on 127.0.0.1.
type fakeAPI struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
	watches  []chan watch.Event
	// watchRequests counts the pod watch requests served.
	watchRequests int
	// watchFailures holds the HTTP statuses the next pod watch requests are answered with, in order.
	watchFailures []int
}

func newFakeAPI(t *testing.T, fixture apiFixture) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.record(r)
		api.serve(w, r, fixture)
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeAPI) URL() string { return a.server.URL }

func (a *fakeAPI) record(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.URL.Path+"?"+r.URL.RawQuery)
}

// requestsTo returns the recorded "path?query" entries whose path ends with suffix.
func (a *fakeAPI) requestsTo(suffix string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var matched []string
	for _, request := range a.requests {
		if strings.HasSuffix(strings.SplitN(request, "?", 2)[0], suffix) {
			matched = append(matched, request)
		}
	}
	return matched
}

// requestsWith returns the requests of requestsTo(suffix) whose query contains fragment.
func (a *fakeAPI) requestsWith(suffix, fragment string) []string {
	var matched []string
	for _, request := range a.requestsTo(suffix) {
		if strings.Contains(request, fragment) {
			matched = append(matched, request)
		}
	}
	return matched
}

func (a *fakeAPI) serve(w http.ResponseWriter, r *http.Request, fixture apiFixture) {
	if status, failing := fixture.failures[r.URL.Path]; failing {
		writeStatus(w, status)
		return
	}

	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case matchPath(segments, "api", "v1", "namespaces"):
		writeJSON(w, namespaceList(fixture.namespaces))
	case matchPath(segments, "api", "v1", "namespaces", "*", "services"):
		writeJSON(w, corev1.ServiceList{
			TypeMeta: metav1.TypeMeta{Kind: "ServiceList", APIVersion: "v1"},
			Items:    fixture.services[segments[3]],
		})
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods") && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods"):
		writeJSON(w, podList(fixture.pods[segments[3]], r.URL.Query().Get("labelSelector")))
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods", "*"):
		serveSinglePod(w, fixture.pods[segments[3]], segments[5])
	case matchPath(segments, "api", "v1", "namespaces", "*", "pods", "*", "portforward") && fixture.forward != nil:
		fixture.forward.ServeHTTP(w, r)
	default:
		writeStatus(w, http.StatusNotFound)
	}
}

func serveSinglePod(w http.ResponseWriter, pods []corev1.Pod, name string) {
	for _, pod := range pods {
		if pod.Name == name {
			writeJSON(w, pod)
			return
		}
	}
	writeStatus(w, http.StatusNotFound)
}

// serveWatch streams the events pushed with publishPodEvent until the client disconnects or closeWatches runs.
func (a *fakeAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	events := make(chan watch.Event, 16)
	a.mu.Lock()
	a.watchRequests++
	if len(a.watchFailures) > 0 {
		status := a.watchFailures[0]
		a.watchFailures = a.watchFailures[1:]
		a.mu.Unlock()
		writeStatus(w, status)
		return
	}
	a.watches = append(a.watches, events)
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flusher.Flush()
	encoder := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			raw, err := json.Marshal(event.Object)
			if err != nil {
				return
			}
			_ = encoder.Encode(metav1.WatchEvent{Type: string(event.Type), Object: runtime.RawExtension{Raw: raw}})
			flusher.Flush()
		}
	}
}

// publishPodEvent sends an event to every open pod watch.
func (a *fakeAPI) publishPodEvent(eventType watch.EventType, pod corev1.Pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pod.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}
	for _, events := range a.watches {
		events <- watch.Event{Type: eventType, Object: &pod}
	}
}

// failNextWatch answers the next pod watch request with the given HTTP status instead of a stream.
func (a *fakeAPI) failNextWatch(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.watchFailures = append(a.watchFailures, status)
}

// closeWatches ends every open pod watch the way an API server timeout does.
func (a *fakeAPI) closeWatches() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, events := range a.watches {
		close(events)
	}
	a.watches = nil
}

// watchCount returns the number of pod watch requests received so far.
func (a *fakeAPI) watchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.watchRequests
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

func namespaceList(names []string) corev1.NamespaceList {
	list := corev1.NamespaceList{TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"}}
	for _, name := range names {
		list.Items = append(list.Items, corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	return list
}

func podList(pods []corev1.Pod, selector string) corev1.PodList {
	list := corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}}
	parsed, err := labels.Parse(selector)
	if err != nil {
		return list
	}
	for _, pod := range pods {
		if parsed.Matches(labels.Set(pod.Labels)) {
			list.Items = append(list.Items, pod)
		}
	}
	return list
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

// writeKubeconfig writes a kubeconfig whose contexts point at the given API server URLs and returns its path.
func writeKubeconfig(t *testing.T, current string, servers map[string]string) string {
	t.Helper()
	config := clientcmdapi.NewConfig()
	for name, server := range servers {
		config.Clusters["cluster-"+name] = &clientcmdapi.Cluster{Server: server}
		config.AuthInfos["user-"+name] = &clientcmdapi.AuthInfo{}
		config.Contexts[name] = &clientcmdapi.Context{Cluster: "cluster-" + name, AuthInfo: "user-" + name}
	}
	config.CurrentContext = current

	path := filepath.Join(t.TempDir(), "config")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func clusterIPService(namespace, name string, selector map[string]string, ports ...corev1.ServicePort) corev1.Service {
	if len(ports) == 0 {
		ports = []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP}}
	}
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.10", Selector: selector, Ports: ports},
	}
}

func runningCorePod(namespace, name string, podLabels map[string]string, ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: podLabels, UID: types.UID("uid-" + name), ResourceVersion: "100"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}},
		}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

// spdyForwardServer upgrades port-forward requests to SPDY and echoes every data stream back to the client.
type spdyForwardServer struct {
	mu    sync.Mutex
	conns []httpstream.Connection
}

func newSPDYForwardServer(t *testing.T) *spdyForwardServer {
	t.Helper()
	server := &spdyForwardServer{}
	t.Cleanup(server.dropConnections)
	return server
}

func (s *spdyForwardServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := httpstream.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
		return
	}
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, echoDataStreams)
	if conn == nil {
		return
	}
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()
}

// dropConnections closes every upgraded connection, which the client sees as a lost pod connection.
func (s *spdyForwardServer) dropConnections() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func echoDataStreams(stream httpstream.Stream, replySent <-chan struct{}) error {
	if stream.Headers().Get(corev1.StreamType) != corev1.StreamTypeData {
		return nil
	}
	go func() {
		<-replySent
		_, _ = io.Copy(stream, stream)
		_ = stream.Close()
	}()
	return nil
}
