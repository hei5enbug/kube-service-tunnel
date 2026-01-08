package kube

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	toolswatch "k8s.io/client-go/tools/watch"
)

const podWatchRetryDelay = 2 * time.Second

// podLossDetector blocks until the pod is lost and returns why, or returns nil once ctx is canceled.
// A non-empty expected UID is the only pod identity that counts as the forwarded pod; when it is empty the
// first pod read becomes the baseline.
type podLossDetector func(ctx context.Context, clientset kubernetes.Interface, namespace, pod string, expected types.UID) error

func watchPodLoss(ctx context.Context, clientset kubernetes.Interface, namespace, pod string, expected types.UID) error {
	watcher := &podWatch{
		pods:        clientset.CoreV1().Pods(namespace),
		namespace:   namespace,
		name:        pod,
		expectedUID: expected,
		retryDelay:  podWatchRetryDelay,
	}
	return watcher.run(ctx)
}

type podWatch struct {
	pods        podAPI
	namespace   string
	name        string
	expectedUID types.UID
	retryDelay  time.Duration
}

type podAPI interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

func (w *podWatch) run(ctx context.Context) error {
	uid := w.expectedUID
	for {
		pod, err := w.read(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if uid == "" {
			uid = pod.UID
		}
		if err := w.check(pod, uid); err != nil {
			return err
		}
		if err := w.follow(ctx, pod.ResourceVersion, uid); err != nil {
			return err
		}
		if !sleepContext(ctx, w.retryDelay) {
			return nil
		}
	}
}

// read returns the current pod, retrying API failures until ctx ends. A missing pod is reported as lost.
func (w *podWatch) read(ctx context.Context) (*corev1.Pod, error) {
	for {
		pod, err := w.pods.Get(ctx, w.name, metav1.GetOptions{})
		if err == nil {
			return pod, nil
		}
		if apierrors.IsNotFound(err) {
			return nil, w.deleted()
		}
		if !sleepContext(ctx, w.retryDelay) {
			return nil, ctx.Err()
		}
	}
}

// follow watches the pod from resourceVersion until it is lost, the watch cannot continue, or ctx ends.
// A nil result means the caller should re-read the pod (or stop when ctx ended). A failure to open the watch
// ends it too, because the retry watcher would otherwise retry forever with the same, possibly expired,
// resourceVersion; the caller's re-read picks a fresh one after its delay.
func (w *podWatch) follow(ctx context.Context, resourceVersion string, uid types.UID) error {
	openFailed := make(chan struct{}, 1)
	retryWatcher, err := toolswatch.NewRetryWatcherWithContext(ctx, resourceVersion, &cache.ListWatch{
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", w.name).String()
			stream, err := w.pods.Watch(ctx, opts)
			if err != nil {
				select {
				case openFailed <- struct{}{}:
				default:
				}
			}
			return stream, err
		},
	})
	if err != nil {
		return nil
	}
	defer retryWatcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-openFailed:
			return nil
		case event, open := <-retryWatcher.ResultChan():
			if !open {
				return nil
			}
			if lost, done := w.handle(event, uid); done {
				return lost
			}
		}
	}
}

// handle returns done when the watch loop must end; lost is the loss reason, or nil to re-read the pod.
func (w *podWatch) handle(event watch.Event, uid types.UID) (lost error, done bool) {
	switch event.Type {
	case watch.Deleted:
		return w.deleted(), true
	case watch.Error:
		return nil, true
	case watch.Added, watch.Modified:
		pod, ok := event.Object.(*corev1.Pod)
		if !ok || pod.Name != w.name {
			return nil, false
		}
		if err := w.check(pod, uid); err != nil {
			return err, true
		}
	}
	return nil, false
}

func (w *podWatch) check(pod *corev1.Pod, uid types.UID) error {
	switch {
	case pod.UID != uid:
		return fmt.Errorf("pod %s/%s was replaced by a new pod", w.namespace, w.name)
	case pod.DeletionTimestamp != nil:
		return fmt.Errorf("pod %s/%s is terminating", w.namespace, w.name)
	case pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded:
		return fmt.Errorf("pod %s/%s is %s", w.namespace, w.name, pod.Status.Phase)
	}
	return nil
}

func (w *podWatch) deleted() error {
	return fmt.Errorf("pod %s/%s was deleted", w.namespace, w.name)
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
