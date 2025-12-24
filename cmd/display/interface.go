package display

import (
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type UIRendererInterface interface {
	GetContexts() []kube.Context
	GetSelectedContext() string

	GetNamespaces() []string
	GetSelectedNamespace() string

	GetServices() []kube.Service

	RefreshNamespaces() error

	SetSelectedContext(contextName string) error
	SetSelectedNamespace(namespace string) error
}
