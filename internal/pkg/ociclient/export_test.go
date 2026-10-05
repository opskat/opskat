package ociclient

import "testing"

// useHTTP 让测试用明文 httptest registry。
func useHTTP(t *testing.T) {
	t.Helper()
	old := scheme
	scheme = "http"
	t.Cleanup(func() { scheme = old })
}
