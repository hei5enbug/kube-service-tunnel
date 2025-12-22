package k8s

func NewContextClient(kubeconfigPath string) (ContextClientInterface, error) {
	return newContextClient(kubeconfigPath)
}

func NewNamespaceClient(kubeconfigPath string) (NamespaceInterface, error) {
	return newNamespaceClient(kubeconfigPath)
}

func NewServiceClient(kubeconfigPath string) (ServiceInterface, error) {
	return newServiceClient(kubeconfigPath)
}

func NewPodClient(kubeconfigPath string) (PodInterface, error) {
	return newPodClient(kubeconfigPath)
}
