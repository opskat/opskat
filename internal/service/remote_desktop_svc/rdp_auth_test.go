package remote_desktop_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubRDPAuthenticator struct {
	address  string
	username string
	password string
}

func (s *stubRDPAuthenticator) Authenticate(_ context.Context, address, username, password string) error {
	s.address = address
	s.username = username
	s.password = password
	if password == "wrong-password" {
		return errors.New("RDP authentication failed")
	}
	return nil
}

func TestRDPAuthenticationUsesCredentialsAndReturnsAuthenticationFailure(t *testing.T) {
	authenticator := &stubRDPAuthenticator{}
	manager := &Manager{rdpAuthenticator: authenticator}

	err := manager.TestRDPAuthentication(
		context.Background(),
		"192.0.2.10:3389",
		nil,
		"DOMAIN\\Administrator",
		"wrong-password",
	)

	require.EqualError(t, err, "RDP authentication failed")
	require.NotEmpty(t, authenticator.address)
	require.Equal(t, "DOMAIN\\Administrator", authenticator.username)
	require.Equal(t, "wrong-password", authenticator.password)
}
