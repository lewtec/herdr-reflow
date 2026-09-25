package herdr

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveLists(t *testing.T) {
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip(err)
	}
	c := &Client{}
	workspaces, err := c.Workspaces(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, workspaces)
	_, err = c.Tabs(t.Context())
	require.NoError(t, err)
	_, err = c.Panes(t.Context())
	require.NoError(t, err)
	sock, err := c.Socket(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, sock)
}
