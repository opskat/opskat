package extension

// hostSession pairs a HostProvider with one invocation's handle table — exactly
// what the runtime assembles for a single WASM call. Tests that exercise host
// IO without a real guest go through it so they observe the same scoping the
// guest does.
type hostSession struct {
	host HostProvider
	inv  *invocation
}

func newHostSession(host HostProvider) *hostSession {
	return &hostSession{host: host, inv: newInvocation("test-invocation", nil)}
}

func (s *hostSession) Open(params IOOpenParams) (uint32, IOMeta, error) {
	res, err := s.host.OpenIO(params)
	if err != nil {
		return 0, IOMeta{}, err
	}
	id, err := s.inv.io.Register(res)
	if err != nil {
		return 0, IOMeta{}, err
	}
	return id, res.Meta, nil
}

func (s *hostSession) Read(id uint32, size int) ([]byte, error) {
	return readHandle(s.inv, id, size)
}

func (s *hostSession) Write(id uint32, data []byte) (int, error) {
	return s.inv.io.Write(id, data)
}

func (s *hostSession) Flush(id uint32) (*IOMeta, error) { return s.inv.io.Flush(id) }

func (s *hostSession) Close(id uint32) error { return s.inv.io.Close(id) }

func (s *hostSession) SetDeadline(id uint32, kind string, unixNanos int64) error {
	return s.inv.io.SetDeadline(id, kind, deadlineTime(unixNanos))
}

func (s *hostSession) CloseAll() { s.inv.close() }
