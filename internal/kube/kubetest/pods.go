package kubetest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

// watchBuffer is how many events one open watch queues before the test fails for not reading them.
const watchBuffer = 256

// watchStream is one open pod watch request. name is empty when the request has no metadata.name selector.
type watchStream struct {
	namespace string
	name      string
	events    chan watch.Event
}

func (w *watchStream) matches(pod *corev1.Pod) bool {
	return w.namespace == pod.Namespace && (w.name == "" || w.name == pod.Name)
}

// GetPod returns a copy of the stored pod.
func (s *Server) GetPod(namespace, name string) (corev1.Pod, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index := s.podIndex(namespace, name); index >= 0 {
		return *s.pods[namespace][index].DeepCopy(), true
	}
	return corev1.Pod{}, false
}

// AddPod stores pod and sends an Added event to the open watches that match it. An empty UID or
// ResourceVersion is filled in with a fresh value. It returns the stored pod. A pod that already exists
// fails the test.
func (s *Server) AddPod(pod corev1.Pod) corev1.Pod {
	s.tb.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.podIndex(pod.Namespace, pod.Name) >= 0 {
		s.tb.Errorf("kubetest: pod %s/%s already exists", pod.Namespace, pod.Name)
		return pod
	}
	stored := *pod.DeepCopy()
	if stored.UID == "" {
		stored.UID = s.newUID(stored.Name)
	}
	if stored.ResourceVersion == "" {
		stored.ResourceVersion = s.nextVersion()
	}
	s.pods[stored.Namespace] = append(s.pods[stored.Namespace], stored)
	s.publish(watch.Added, &stored)
	return *stored.DeepCopy()
}

// UpdatePod applies change to a copy of the stored pod, bumps its resourceVersion, stores it and sends a
// Modified event. The UID stays the same. It returns the stored pod. A missing pod fails the test.
func (s *Server) UpdatePod(namespace, name string, change func(*corev1.Pod)) corev1.Pod {
	s.tb.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewrite(namespace, name, func(pod *corev1.Pod) {
		change(pod)
		pod.ResourceVersion = s.nextVersion()
	})
}

// ReplacePod stores pod in place of the pod with the same namespace and name, as if the pod had been
// recreated while no client was watching: it gets a new UID and a bumped resourceVersion, and a Modified
// event carries it. The pod is added when none exists. It returns the stored pod.
func (s *Server) ReplacePod(pod corev1.Pod) corev1.Pod {
	s.tb.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	replacement := *pod.DeepCopy()
	replacement.UID = s.newUID(replacement.Name)
	replacement.ResourceVersion = s.nextVersion()
	index := s.podIndex(replacement.Namespace, replacement.Name)
	if index < 0 {
		s.pods[replacement.Namespace] = append(s.pods[replacement.Namespace], replacement)
		s.publish(watch.Added, &replacement)
		return *replacement.DeepCopy()
	}
	s.pods[replacement.Namespace][index] = replacement
	s.publish(watch.Modified, &replacement)
	return *replacement.DeepCopy()
}

// DeletePod removes the pod and sends a Deleted event carrying its last state with a bumped resourceVersion.
// Port-forward connections that are already open stay open, as they do on a real kubelet; only new
// port-forward requests for the pod are refused. A missing pod fails the test.
func (s *Server) DeletePod(namespace, name string) corev1.Pod {
	s.tb.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.podIndex(namespace, name)
	if index < 0 {
		s.tb.Fatalf("kubetest: pod %s/%s does not exist", namespace, name)
		return corev1.Pod{}
	}
	deleted := *s.pods[namespace][index].DeepCopy()
	deleted.ResourceVersion = s.nextVersion()
	s.pods[namespace] = append(s.pods[namespace][:index:index], s.pods[namespace][index+1:]...)
	s.publish(watch.Deleted, &deleted)
	return *deleted.DeepCopy()
}

// PublishPodEvent sends an event to the open watches that match pod without changing the stored pods. Use it
// for event sequences the other helpers cannot express.
func (s *Server) PublishPodEvent(eventType watch.EventType, pod corev1.Pod) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publish(eventType, pod.DeepCopy())
}

func (s *Server) rewrite(namespace, name string, change func(*corev1.Pod)) corev1.Pod {
	s.tb.Helper()
	index := s.podIndex(namespace, name)
	if index < 0 {
		s.tb.Fatalf("kubetest: pod %s/%s does not exist", namespace, name)
		return corev1.Pod{}
	}
	updated := *s.pods[namespace][index].DeepCopy()
	change(&updated)
	s.pods[namespace][index] = updated
	s.publish(watch.Modified, &updated)
	return *updated.DeepCopy()
}

// podIndex returns the position of the pod in its namespace, or -1. Callers hold s.mu.
func (s *Server) podIndex(namespace, name string) int {
	for i := range s.pods[namespace] {
		if s.pods[namespace][i].Name == name {
			return i
		}
	}
	return -1
}

func (s *Server) nextVersion() string {
	s.version++
	return strconv.FormatInt(s.version, 10)
}

func (s *Server) newUID(name string) types.UID {
	s.uids++
	return types.UID("uid-" + name + "-" + strconv.Itoa(s.uids))
}

// publish queues the event on every matching watch. Callers hold s.mu.
func (s *Server) publish(eventType watch.EventType, pod *corev1.Pod) {
	sent := pod.DeepCopy()
	sent.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}
	for stream := range s.watches {
		if !stream.matches(sent) {
			continue
		}
		select {
		case stream.events <- watch.Event{Type: eventType, Object: sent}:
		default:
			s.tb.Errorf("kubetest: pod watch buffer is full; the client does not read events")
		}
	}
}

// FailNextWatch answers the next pod watch request with the HTTP status instead of a stream. Statuses queued
// by several calls are used by the following requests in call order.
func (s *Server) FailNextWatch(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchFailures = append(s.watchFailures, status)
}

// CloseWatches ends every open pod watch the way an API server timeout does.
func (s *Server) CloseWatches() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for stream := range s.watches {
		close(stream.events)
		delete(s.watches, stream)
	}
	s.watchSignal.fire()
}

// WatchCount returns how many pod watch requests the server has received, failed ones included.
func (s *Server) WatchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watchRequests
}

// OpenWatches returns how many pod watch streams are open right now.
func (s *Server) OpenWatches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watches)
}

// WaitForOpenWatches blocks until exactly count pod watch streams are open, and fails tb after a generous
// timeout. Wait for 0 to know that a client has stopped watching. Call it from the test goroutine.
func (s *Server) WaitForOpenWatches(tb testing.TB, count int) {
	tb.Helper()
	s.waitFor(tb, &s.watchSignal, func() bool { return len(s.watches) == count }, strconv.Itoa(count)+" open pod watches")
}

// WaitForWatches blocks until WatchCount reaches count, and fails tb after a generous timeout. A watch
// counted here is registered, so an event published afterwards reaches it. Call it from the test goroutine.
func (s *Server) WaitForWatches(tb testing.TB, count int) {
	tb.Helper()
	s.waitFor(tb, &s.watchSignal, func() bool { return s.watchRequests >= count }, "pod watch #"+strconv.Itoa(count))
}

// serveWatch streams events published for the namespace until the client disconnects, CloseWatches runs, or
// the server shuts down.
func (s *Server) serveWatch(w http.ResponseWriter, r *http.Request, namespace string) {
	stream := &watchStream{
		namespace: namespace,
		name:      selectedName(r.URL.Query().Get("fieldSelector")),
		events:    make(chan watch.Event, watchBuffer),
	}
	if status, failed := s.registerWatch(stream); failed {
		writeStatus(w, status)
		return
	}
	defer s.unregisterWatch(stream)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flusher.Flush()
	encoder := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-stream.events:
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

// registerWatch counts the request and either reports the status queued by FailNextWatch or registers the
// stream. A server that is shutting down answers 503.
func (s *Server) registerWatch(stream *watchStream) (status int, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchRequests++
	defer s.watchSignal.fire()
	if s.closed {
		return http.StatusServiceUnavailable, true
	}
	if len(s.watchFailures) > 0 {
		status = s.watchFailures[0]
		s.watchFailures = s.watchFailures[1:]
		return status, true
	}
	s.watches[stream] = struct{}{}
	return 0, false
}

func (s *Server) unregisterWatch(stream *watchStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.watches, stream)
	s.watchSignal.fire()
}

func selectedName(fieldSelector string) string {
	selector, err := fields.ParseSelector(fieldSelector)
	if err != nil {
		return ""
	}
	name, _ := selector.RequiresExactMatch("metadata.name")
	return name
}
