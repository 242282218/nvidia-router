package runtimeconfig

// Snapshot is the runtime configuration read once at the beginning of a request.
type Snapshot struct {
	QueueCapacity           int
	QueueWaitTimeoutMS      int
	ConnectTimeoutMS        int
	FirstByteTimeoutMS      int
	NonstreamTotalTimeoutMS int
	ShutdownGraceMS         int
}

// Provider exposes an immutable configuration snapshot to request handlers.
type Provider interface {
	Snapshot() Snapshot
}
