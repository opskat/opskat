package kerberos

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jcmturner/gokrb5/v8/iana/errorcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/kerberos/kerberostest"
)

const (
	realm    = "EXAMPLE.TEST"
	password = "alice-secret-pw"
	spn      = "oracle/db.example.test"
)

func newKDC(t *testing.T) *kerberostest.KDC {
	kdc := kerberostest.New(t, realm)
	kdc.AddUser("alice", password)
	kdc.AddService(spn)
	kdc.AddService("oracle/other.example.test")
	return kdc
}

// router 是调用方给的拨号途径：按地址表把 KDC 地址接到假 KDC，记下每次拨的地址；表外的地址拨不通。
type router struct {
	mu     sync.Mutex
	routes map[string]string
	dialed []string
}

func (r *router) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.dialed = append(r.dialed, network+" "+addr)
	target, ok := r.routes[addr]
	r.mu.Unlock()
	if !ok {
		return nil, errors.New("connection refused")
	}
	var d net.Dialer
	return d.DialContext(ctx, network, target)
}

func (r *router) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

func passwordSettings() Settings {
	return Settings{Principal: "alice@" + realm, Password: password, KDCs: []string{"kdc.example.test"}}
}

func TestTicketWithPasswordYieldsAPReqForSPN(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}

	b, err := NewCache().Ticket(context.Background(), 1, passwordSettings(), spn, r.dial)
	require.NoError(t, err)

	got, err := kdc.VerifyAPReq(b)
	require.NoError(t, err)
	assert.Equal(t, kerberostest.APReq{Client: "alice@" + realm, SPN: spn}, got)
	// 主机名原样交给拨号途径（由它在远端解析），默认端口 88，只用 TCP
	for _, call := range r.calls() {
		assert.Equal(t, "tcp kdc.example.test:88", call)
	}
}

func TestTicketWithKeytab(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}
	s := Settings{Principal: "alice@" + realm, KeytabFile: kdc.WriteKeytab(t, "alice"), KDCs: []string{"kdc.example.test:88"}}

	b, err := NewCache().Ticket(context.Background(), 1, s, spn, r.dial)
	require.NoError(t, err)
	got, err := kdc.VerifyAPReq(b)
	require.NoError(t, err)
	assert.Equal(t, "alice@"+realm, got.Client)
}

func TestKeytabProblemsNameTheField(t *testing.T) {
	kdc := newKDC(t)
	kdc.AddUser("bob", "bob-pw")
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}

	missing := filepath.Join(t.TempDir(), "missing.keytab")
	s := Settings{Principal: "alice@" + realm, KeytabFile: missing, KDCs: []string{"kdc.example.test"}}
	_, err := NewCache().Ticket(context.Background(), 1, s, spn, r.dial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keytab file")
	assert.Contains(t, err.Error(), missing)

	s.KeytabFile = kdc.WriteKeytab(t, "bob")
	_, err = NewCache().Ticket(context.Background(), 1, s, spn, r.dial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keytab file")
	assert.Contains(t, err.Error(), "alice@"+realm)
	assert.Empty(t, r.calls(), "a keytab without the principal's key must fail before contacting a KDC")
}

func TestKDCsTriedInOrderFirstReachableWins(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc2.example.test:88": kdc.Addr(), "kdc3.example.test:88": kdc.Addr()}}
	s := passwordSettings()
	s.KDCs = []string{"kdc1.example.test", "kdc2.example.test:88", "kdc3.example.test"}

	_, err := NewCache().Ticket(context.Background(), 1, s, spn, r.dial)
	require.NoError(t, err)
	calls := r.calls()
	require.NotEmpty(t, calls)
	assert.Equal(t, []string{"tcp kdc1.example.test:88", "tcp kdc2.example.test:88"}, calls[:2])
	assert.NotContains(t, calls, "tcp kdc3.example.test:88")
}

func TestUnreachableKDCsAreAllNamed(t *testing.T) {
	r := &router{routes: map[string]string{}}
	s := passwordSettings()
	s.KDCs = []string{"kdc1.example.test", "10.0.0.9:750"}

	_, err := NewCache().Ticket(context.Background(), 1, s, spn, r.dial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kdc1.example.test:88")
	assert.Contains(t, err.Error(), "10.0.0.9:750")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestKDCErrorsAreReadable(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Settings, *kerberostest.KDC) string
		want   string
	}{
		{"wrong password", func(s *Settings, _ *kerberostest.KDC) string { s.Password = "wrong-pw"; return spn }, "KDC_ERR_PREAUTH_FAILED"},
		{"unknown principal", func(s *Settings, _ *kerberostest.KDC) string { s.Principal = "nobody@" + realm; return spn }, "KDC_ERR_C_PRINCIPAL_UNKNOWN"},
		{"unknown service", func(*Settings, *kerberostest.KDC) string { return "oracle/nowhere.example.test" }, "KDC_ERR_S_PRINCIPAL_UNKNOWN"},
		{"clock skew", func(_ *Settings, k *kerberostest.KDC) string { k.FailWith(errorcode.KRB_AP_ERR_SKEW); return spn }, "KRB_AP_ERR_SKEW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kdc := newKDC(t)
			r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}
			s := passwordSettings()
			target := tc.mutate(&s, kdc)

			_, err := NewCache().Ticket(context.Background(), 1, s, target, r.dial)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "kdc.example.test:88")
			assert.NotContains(t, err.Error(), s.Password)
		})
	}
}

func TestSettingsProblems(t *testing.T) {
	r := &router{routes: map[string]string{}}
	cases := map[string]struct {
		s    Settings
		want string
	}{
		"principal without realm": {Settings{Principal: "alice", Password: password, KDCs: []string{"k"}}, "user@REALM"},
		"no KDC":                  {Settings{Principal: "alice@" + realm, Password: password}, "KDC"},
		"no credential":           {Settings{Principal: "alice@" + realm, KDCs: []string{"k"}}, "password or a keytab file"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewCache().Ticket(context.Background(), 1, tc.s, spn, r.dial)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	assert.Empty(t, r.calls())
}

func TestTicketsCachedPerAssetUntilExpiry(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}
	c := NewCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	first, err := c.Ticket(ctx, 1, passwordSettings(), spn, r.dial)
	require.NoError(t, err)
	as, tgs := kdc.ASRequests(), kdc.TGSRequests()
	assert.Equal(t, 1, tgs)

	second, err := c.Ticket(ctx, 1, passwordSettings(), spn, r.dial)
	require.NoError(t, err)
	assert.Equal(t, as, kdc.ASRequests(), "cached TGT reused")
	assert.Equal(t, tgs, kdc.TGSRequests(), "cached service ticket reused")
	assert.NotEqual(t, first, second, "each AP-REQ carries a fresh authenticator")
	_, err = kdc.VerifyAPReq(second)
	require.NoError(t, err)

	// 同一资产的另一个服务主体：沿用 TGT，只取服务票据
	other, err := c.Ticket(ctx, 1, passwordSettings(), "oracle/other.example.test", r.dial)
	require.NoError(t, err)
	got, err := kdc.VerifyAPReq(other)
	require.NoError(t, err)
	assert.Equal(t, "oracle/other.example.test", got.SPN)
	assert.Equal(t, as, kdc.ASRequests())
	assert.Equal(t, tgs+1, kdc.TGSRequests())

	// 另一个资产：自己取 TGT
	_, err = c.Ticket(ctx, 2, passwordSettings(), spn, r.dial)
	require.NoError(t, err)
	assert.Equal(t, 2*as, kdc.ASRequests())

	// 票据过期后重取
	now = now.Add(11 * time.Hour)
	_, err = c.Ticket(ctx, 1, passwordSettings(), spn, r.dial)
	require.NoError(t, err)
	assert.Equal(t, 3*as, kdc.ASRequests())
	assert.Equal(t, tgs+3, kdc.TGSRequests())
}

func TestSettingsChangeInvalidatesCache(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr(), "kdc2.example.test:88": kdc.Addr()}}
	c := NewCache()
	ctx := context.Background()

	_, err := c.Ticket(ctx, 1, passwordSettings(), spn, r.dial)
	require.NoError(t, err)
	as, tgs := kdc.ASRequests(), kdc.TGSRequests()

	changed := passwordSettings()
	changed.KDCs = []string{"kdc2.example.test"}
	_, err = c.Ticket(ctx, 1, changed, spn, r.dial)
	require.NoError(t, err)
	assert.Equal(t, 2*as, kdc.ASRequests())
	assert.Equal(t, 2*tgs, kdc.TGSRequests())

	// 改错口令后不能再用旧票据
	changed.Password = "wrong-pw"
	_, err = c.Ticket(ctx, 1, changed, spn, r.dial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KDC_ERR_PREAUTH_FAILED")
}

func TestConcurrentRequestsForOneAssetFetchOnce(t *testing.T) {
	kdc := newKDC(t)
	r := &router{routes: map[string]string{"kdc.example.test:88": kdc.Addr()}}
	c := NewCache()

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := c.Ticket(context.Background(), 1, passwordSettings(), spn, r.dial)
			if err == nil {
				_, err = kdc.VerifyAPReq(b)
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 2, kdc.ASRequests(), "one AS exchange (pre-auth round trip)")
	assert.Equal(t, 1, kdc.TGSRequests())
}
