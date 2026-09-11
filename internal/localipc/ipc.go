// Package localipc transports desktop/CLI streams over Unix sockets or
// user-restricted Windows named pipes. Callers supply a logical socket path;
// only this package knows the platform's actual address and lifecycle.
package localipc

import (
	"context"
	"net"
	"time"
)

// Dial bounds connection establishment only. Approval responses can legitimately
// take minutes while a person reads the dialog; no response deadline is added.
func Dial(path string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return DialContext(ctx, path)
}
