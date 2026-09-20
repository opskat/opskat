package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenK8sPodLogsBufferRejectsEmpty(t *testing.T) {
	k := &K8s{}
	require.Error(t, k.OpenK8sPodLogsBuffer("", "default", "pod-a", "main"))
	require.Error(t, k.OpenK8sPodLogsBuffer("   \n", "default", "pod-a", "main"))
}
