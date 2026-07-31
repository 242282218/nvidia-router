package v1

import "nvidia-router/internal/runtimeconfig"

type testNVIDIASettings struct{}

func (testNVIDIASettings) Snapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
}
