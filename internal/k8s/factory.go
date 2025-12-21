package k8s

// NewContext creates a new ContextLister interface instance
func NewContext(kubeconfigPath string) (ContextLister, error) {
	return newContextClient(kubeconfigPath)
}

// NewNamespace creates a new Namespace interface instance
func NewNamespace(kubeconfigPath string) (Namespace, error) {
	return newNamespaceClient(kubeconfigPath)
}

// NewService creates a new ServiceLister interface instance
func NewService(kubeconfigPath string) (ServiceLister, error) {
	return newServiceClient(kubeconfigPath)
}
