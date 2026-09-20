package desktopopen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenRejectsEmptyPath(t *testing.T) {
	require.Error(t, Open(""))
	require.Error(t, Open("   "))
}
