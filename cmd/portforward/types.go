package portforward

type PortForward struct {
	Key        string
	Context    string
	Namespace  string
	Pod        string
	LocalPort  int32
	RemotePort int32
	StopCh     chan struct{}
	ReadyCh    chan struct{}
	ErrorCh    chan error
}

