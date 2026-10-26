package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension/driverwire"
)

// A database driver an extension provides runs in the guest: the SDK assembles
// its session there (pkg/extsdk/sdkdbdriver DatabaseDriver), and the host holds a
// dbdriver.Session that forwards every operation across the boundary as JSON
// (pkg/extension/driverwire). To shared code it is a session like any built-in
// driver's.
//
// A session runs on up to driverSessionInstances module instances of its own,
// outside the plugin's call pool. Each instance logs in once — its own session in
// the guest, its own database connection — and keeps that session, its
// connection and its cursors in its memory between calls; its invocation, with
// the IO handle table that holds the connection, lasts as long as the instance
// instead of one call. A WASM instance runs one call at a time, so an operation
// holds an instance for as long as it runs: operations beyond the instances run
// on a new one, logged in on demand, or — once the session has all of them —
// wait for one in arrival order. A cursor holds its instance from open to close.
//
// The host, not the guest, ends a call that is canceled or past its deadline: a
// guest blocked in a host read never gets to run its own timers. Closing the
// instance's connection fails that read; the instance is then in an unknown
// state and is discarded, as is one whose call panicked, trapped or ran out of
// memory. The next operation that needs an instance logs in a new one.

const (
	// driverSessionInstances is how many instances, and so database connections,
	// one session runs at once.
	driverSessionInstances = 3
	// driverStreamRows is how many rows one cursor read brings over.
	driverStreamRows = 256
)

// errDriverSessionClosed is what an operation on a closed session fails with.
var errDriverSessionClosed = errors.New("database driver session is closed")

// OpenDriverSession opens a session of database driver id, which this extension
// must declare in capabilities.databaseDrivers. req is handed to the driver — its
// config, the decrypted password, the driver options, the TLS material and the
// reach path, which every TCP connection the driver opens takes — each time an
// instance of the session logs in. A driver that cannot connect fails with its
// own error.
//
// The session's calls are bounded only by ctx — the asset's query timeout and
// the caller's cancellation — not by the plugin's tool timeout.
func (p *Plugin) OpenDriverSession(ctx context.Context, id string, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("plugin closed")
	}
	if !slices.Contains(p.manifest.Capabilities.DatabaseDrivers, id) {
		return nil, fmt.Errorf("extension %q does not provide database driver %q", p.manifest.Name, id)
	}
	if req.Config != nil {
		// The SSH asset is the host's: a tunnel it has already resolved into
		// req.Transport (or a file host only a built-in SQLite reaches). It is not
		// part of the config the extension is handed.
		cfg := *req.Config
		cfg.SSHAssetID = 0
		req.Config = &cfg
	}
	if req.Kerberos != nil {
		// A Kerberos asset logs in with the tickets the host fetches; its password
		// is no more the extension's than the Kerberos credentials are.
		req.Password = ""
	}
	s := &driverSession{plugin: p, driver: id, open: req, live: 1}
	s.reach = p.confineReach(id, &s.open)
	s.tickets = p.ticketGate(id, &s.open)
	inst, err := s.login(ctx)
	if err != nil {
		return nil, err
	}
	s.release(inst)
	return s, nil
}

// ClassifyStatements splits and classifies sql with database driver id
// (dbdriver.StatementClassifier), which this extension must declare in
// capabilities.databaseDrivers. No session is involved: the driver does not touch
// the database to answer, so the call runs on a pooled instance like
// CheckPolicy, with no asset, no reach path and the plugin's tool timeout. The
// statements are the driver's as it answered them; dbdriver.ClassifyStatements
// checks them. A failure the driver answers — text it cannot split — wraps
// dbdriver.ErrCannotSplit with the driver's own reason.
func (p *Plugin) ClassifyStatements(ctx context.Context, id, sql string) ([]dbdriver.Statement, error) {
	if !slices.Contains(p.manifest.Capabilities.DatabaseDrivers, id) {
		return nil, fmt.Errorf("extension %q does not provide database driver %q", p.manifest.Name, id)
	}
	input, err := json.Marshal(driverwire.Call{Op: driverwire.OpClassifyStatements, Args: driverwire.Args{
		Driver: dbdriver.DriverID(id), Statement: sql,
	}})
	if err != nil {
		return nil, fmt.Errorf("marshal database driver %s: %w", driverwire.OpClassifyStatements, err)
	}
	raw, err := p.call(ctx, newInvocation(p.nextInvocationID(), nil), driverwire.Function, input, p.opts.toolTimeout)
	if err != nil {
		return nil, err
	}
	var reply driverwire.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("parse database driver %s reply: %w", driverwire.OpClassifyStatements, err)
	}
	if reply.Error != nil {
		return nil, fmt.Errorf("%w: %w", dbdriver.ErrCannotSplit, &driverError{message: reply.Error.Message, code: reply.Error.Code})
	}
	var stmts []dbdriver.Statement
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &stmts); err != nil {
			return nil, fmt.Errorf("parse database driver %s result: %w", driverwire.OpClassifyStatements, err)
		}
	}
	return stmts, nil
}

// openDriverInstance creates an instance and logs it in: the driver opens its
// session in the guest and connects, every TCP stream taking the session's
// confined reach path and every Kerberos ticket the session's ticket gate.
func (p *Plugin) openDriverInstance(ctx context.Context, driver string, req *dbdriver.OpenRequest, reach *dbdriver.Transport, tickets *ticketGate) (*driverInstance, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("plugin closed")
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		return nil, err
	}
	inv := newInvocation(p.nextInvocationID(), nil)
	inv.reach = reach
	inv.tickets = tickets
	d := &driverInstance{plugin: p, driver: driver, inst: inst, inv: inv}
	d.refs.Store(1)

	var opened driverwire.Opened
	err = d.call(ctx, driverwire.Call{Op: driverwire.OpOpen, Args: driverwire.Args{
		Driver: dbdriver.DriverID(driver), Config: req.Config, Password: req.Password, DriverOptions: req.DriverOptions,
		TLS: req.TLS, Kerberos: req.Kerberos != nil,
	}}, &opened)
	if err != nil {
		d.inv.close()
		d.drop()
		logger.Ctx(ctx).Warn("extension database driver instance login failed",
			zap.String("extension", p.manifest.Name), zap.String("driver", driver), zap.String("invocation", inv.id), zap.Error(err))
		return nil, err
	}
	d.session = opened.Session
	logger.Ctx(ctx).Info("extension database driver instance logged in",
		zap.String("extension", p.manifest.Name), zap.String("driver", driver), zap.String("invocation", inv.id))
	return d, nil
}

// confineReach is the reach path every instance of a session dials through: the
// session's own, narrowed by a reachGate. The asset's own address takes the
// route's Dial, as a built-in driver's connection does. Any other address is
// refused unless the extension declares capabilities.network.databaseRedirects;
// then it is a redirect the database answered with, dialed by address along the
// same route (see reachGate.redirect). A refused address is named in the error
// the guest gets and logged; the logs carry no credential, only the addresses.
// A route without a dial of its own dials directly, as a TCP stream of any other
// host call does.
func (p *Plugin) confineReach(driver string, req *dbdriver.OpenRequest) *dbdriver.Transport {
	g := &reachGate{
		extension: p.manifest.Name,
		driver:    driver,
		assetID:   req.AssetID,
		redirects: p.manifest.Capabilities.Network.DatabaseRedirects,
		// No by-address dial means a direct one (orDirectDial): it gets the direct
		// route's check whatever Direct says.
		direct:   req.Transport.Direct || req.Transport.DialAddr == nil,
		dial:     orDirectDial(req.Transport.Dial),
		dialAddr: orDirectDial(req.Transport.DialAddr),
		followed: map[string]bool{},
	}
	if cfg := req.Config; cfg != nil {
		g.assetHost = cfg.Host
		g.allowed = net.JoinHostPort(canonicalHost(cfg.Host), strconv.Itoa(cfg.Port))
	}
	return &dbdriver.Transport{Dial: g.dialContext}
}

func orDirectDial(dial dbdriver.DialFunc) dbdriver.DialFunc {
	if dial == nil {
		return (&netdial.Dialer{}).DialContext
	}
	return dial
}

// reachGate decides, for one session, which addresses its driver may dial and
// along what.
type reachGate struct {
	extension, driver string
	assetID           int64
	// assetHost is the asset's host as configured; allowed is its host:port as
	// sameEndpoint compares it.
	assetHost, allowed string
	// redirects is the extension's capabilities.network.databaseRedirects.
	redirects bool
	// direct reports a direct route: names resolve on this machine.
	direct         bool
	dial, dialAddr dbdriver.DialFunc

	mu sync.Mutex
	// followed are the redirect addresses the session has dialed, logged on the
	// first dial of each.
	followed map[string]bool
}

func (g *reachGate) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if sameEndpoint(g.allowed, addr) {
		return g.dial(ctx, network, addr)
	}
	if !g.redirects {
		g.deny(ctx, addr)
		return nil, fmt.Errorf("dial denied: %q is not the address of the database asset (%s), and extension %q does not declare following database redirects (capabilities.network.databaseRedirects)",
			addr, g.allowed, g.extension)
	}
	return g.redirect(ctx, network, addr)
}

func (g *reachGate) deny(ctx context.Context, addr string) {
	logger.Ctx(ctx).Warn("extension database driver dial denied",
		zap.String("extension", g.extension), zap.String("driver", g.driver), zap.Int64("assetID", g.assetID),
		zap.String("addr", addr), zap.String("allowed", g.allowed))
}

// redirect dials an address other than the asset's along the asset's route, by
// address: through an SSH tunnel to that address itself, its name resolved at the
// far end of a tunnel, proxy or proxy chain. On a direct route the name resolves
// here, and an address on this machine (loopback, unspecified) or on its link
// (link-local) is refused — a database the user reaches directly cannot point the
// driver at the user's own machine — unless the asset itself is such an address.
// What is dialed is the address checked, not the name resolved again.
func (g *reachGate) redirect(ctx context.Context, network, addr string) (net.Conn, error) {
	targets := []string{addr}
	if g.direct {
		var err error
		if targets, err = g.directTargets(ctx, network, addr); err != nil {
			return nil, err
		}
	}
	g.logFirst(ctx, addr)
	var errs []error
	for _, target := range targets {
		conn, err := g.dialAddr(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("dial %s: %w", addr, errors.Join(errs...))
}

// logFirst records the session's first dial of a redirect address.
func (g *reachGate) logFirst(ctx context.Context, addr string) {
	key := addr
	if host, port, err := net.SplitHostPort(addr); err == nil {
		key = net.JoinHostPort(canonicalHost(host), port)
	}
	g.mu.Lock()
	first := !g.followed[key]
	g.followed[key] = true
	g.mu.Unlock()
	if first {
		logger.Ctx(ctx).Info("extension database driver follows a database redirect",
			zap.String("extension", g.extension), zap.String("driver", g.driver), zap.Int64("assetID", g.assetID),
			zap.String("addr", addr))
	}
}

// directTargets are the addresses to dial for addr on a direct route: addr
// itself when it and the asset are both on this machine or its link, else the
// resolved ip:port addresses — none of them on this machine or its link.
func (g *reachGate) directTargets(ctx context.Context, network, addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveHere(ctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	if slices.ContainsFunc(ips, onThisMachineOrLink) {
		assetIPs, err := resolveHere(ctx, network, g.assetHost)
		if err != nil {
			return nil, fmt.Errorf("dial %s: resolve the database asset's host: %w", addr, err)
		}
		if slices.ContainsFunc(assetIPs, onThisMachineOrLink) {
			return []string{addr}, nil
		}
		g.deny(ctx, addr)
		return nil, fmt.Errorf("dial denied: %q is a loopback or link-local address, which a database redirect on a direct route may not reach (the database asset is %s)",
			addr, g.allowed)
	}
	targets := make([]string, len(ips))
	for i, ip := range ips {
		targets[i] = net.JoinHostPort(ip.String(), port)
	}
	return targets, nil
}

// resolveHere resolves host on this machine, the way a direct dial would.
func resolveHere(ctx context.Context, network, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return (&netdial.Dialer{}).Resolve(ctx, network, host)
}

// onThisMachineOrLink reports a loopback or link-local address, or the
// unspecified one, which a dial reaches as this machine.
func onThisMachineOrLink(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// sameEndpoint reports whether addr names the host:port allowed, hosts compared
// the way asset endpoints are (case folded, IP literals spelled one way).
func sameEndpoint(allowed, addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return allowed == net.JoinHostPort(canonicalHost(host), port)
}

// errTicketOutsideDriverSession refuses a Kerberos ticket request from a call
// that is not a database driver session's.
var errTicketOutsideDriverSession = errors.New("kerberos ticket refused: this call is not inside a database driver session; only a driver logging in to its Kerberos asset may ask for tickets")

// ticketGate is how every instance of a session reaches the asset's Kerberos
// tickets: the ticket source the caller of OpenDriverSession supplied, nil when
// the asset does not log in with Kerberos. The guest only names the service
// principal; the gate refuses one that is not service/host or that differs from
// the asset's pinned one, and logs the session's first ticket for each — the
// principal and SPN only, never a credential or a ticket.
func (p *Plugin) ticketGate(driver string, req *dbdriver.OpenRequest) *ticketGate {
	return &ticketGate{
		extension: p.manifest.Name,
		driver:    driver,
		assetID:   req.AssetID,
		source:    req.Kerberos,
		issued:    map[string]bool{},
	}
}

type ticketGate struct {
	extension, driver string
	assetID           int64
	source            *dbdriver.Kerberos

	mu sync.Mutex
	// issued are the SPNs the session has been given a ticket for, logged on the
	// first of each.
	issued map[string]bool
}

func (g *ticketGate) ticket(ctx context.Context, spn string) ([]byte, error) {
	if g.source == nil {
		return nil, fmt.Errorf("kerberos ticket refused: database asset %d does not log in with Kerberos", g.assetID)
	}
	if err := dbdriver.CheckKerberosSPN(spn); err != nil {
		return nil, fmt.Errorf("kerberos ticket refused: %w", err)
	}
	if g.source.SPN != "" && spn != g.source.SPN {
		return nil, fmt.Errorf("kerberos ticket refused: the driver asked for service principal %q, but the database asset is pinned to %q", spn, g.source.SPN)
	}
	ticket, err := g.source.Ticket(ctx, spn)
	if err != nil {
		return nil, fmt.Errorf("kerberos ticket for %s: %w", spn, err)
	}
	g.mu.Lock()
	first := !g.issued[spn]
	g.issued[spn] = true
	g.mu.Unlock()
	if first {
		logger.Ctx(ctx).Info("extension database driver got a Kerberos ticket",
			zap.String("extension", g.extension), zap.String("driver", g.driver), zap.Int64("assetID", g.assetID),
			zap.String("principal", g.source.Principal), zap.String("spn", spn))
	}
	return ticket, nil
}

// driverInstance is one module instance of a session, logged in to the database.
// Calls into it are serialized: an instance runs one call at a time.
//
// It is reference counted because a prepared row statement writer outlives its
// session (dbdriver.Session.PrepareRowStatements) and generates in the guest: the
// module is closed once the session has let go of the instance and every writer
// prepared on it has been collected.
type driverInstance struct {
	plugin *Plugin
	driver string
	inst   *instance
	inv    *invocation
	// session is the instance's session in the guest.
	session uint64

	// idleGen and idleTimer belong to the session (guarded by its mu): the
	// instance's current idle spell, and the timer that closes it if the spell
	// lasts. A timer from an earlier spell finds idleGen moved on.
	idleGen   uint64
	idleTimer *time.Timer

	// refs counts the session's hold on the instance and each writer prepared on
	// it. It is not under mu: a writer's cleanup must not wait for a call.
	refs atomic.Int32

	// released are the writers prepared on the instance that their holders have let
	// go of; the next call hands them to the guest to forget.
	releasedMu sync.Mutex
	released   []uint64

	mu sync.Mutex
	// broken is the failure that left the instance in an unknown state (a trap, a
	// canceled call); every later call fails with it.
	broken error
}

func (d *driverInstance) hold() { d.refs.Add(1) }

// drop lets go of one hold; the last closes the module. No call can be running
// then: calls are made by the session, which holds the instance until it retires
// it, and by writers, which hold it while they are reachable.
func (d *driverInstance) drop() {
	if d.refs.Add(-1) > 0 {
		return
	}
	if err := d.inst.mod.Close(context.Background()); err != nil {
		logger.Default().Warn("close database driver instance", zap.Error(err))
	}
}

// releaseWriter is a writer's cleanup: the guest forgets the writer with the next
// call on the instance, and the writer's hold on the instance goes.
func (d *driverInstance) releaseWriter(id uint64) {
	d.releasedMu.Lock()
	d.released = append(d.released, id)
	d.releasedMu.Unlock()
	d.drop()
}

// takeReleased hands over the writers to release with the next call.
func (d *driverInstance) takeReleased() []uint64 {
	d.releasedMu.Lock()
	defer d.releasedMu.Unlock()
	ids := d.released
	d.released = nil
	return ids
}

func (d *driverInstance) usable() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.broken == nil
}

// discard marks the instance unusable and closes its connections.
func (d *driverInstance) discard(reason error) {
	d.mu.Lock()
	if d.broken == nil {
		d.broken = reason
	}
	d.mu.Unlock()
	d.inv.close()
}

// retire closes the instance's session in the guest — when the instance is
// still usable — and its connections, and lets go of the instance; a writer
// prepared on it keeps the module until the writer is collected. Closing in the
// guest is bounded by the plugin's driver close timeout: a driver's close may
// wait on a database that no longer answers, and retiring runs when an extension
// is disabled or removed and when an idle instance is reaped.
func (d *driverInstance) retire() error {
	var err error
	if d.usable() {
		ctx, cancel := context.WithTimeout(context.Background(), d.plugin.opts.driverCloseTimeout)
		err = d.call(ctx, driverwire.Call{Op: driverwire.OpClose, Session: d.session}, nil)
		cancel()
	}
	d.inv.close()
	d.drop()
	logger.Default().Info("extension database driver instance closed",
		zap.String("extension", d.plugin.manifest.Name), zap.String("driver", d.driver), zap.String("invocation", d.inv.id))
	return err
}

// call runs one operation and decodes its result into out (when the reply has
// one). The operation's own failure is returned as a driverError, after the
// result: a batch that failed part way answers both.
//
// ctx ending while the call runs closes the instance's connections — the guest
// may be blocked reading one — and fails the call with ctx's error. A call that
// fails in the guest itself (a panic, a trap, running out of memory) fails as an
// extension database driver fault. Either way the instance is left unusable.
func (d *driverInstance) call(ctx context.Context, call driverwire.Call, out any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.broken != nil {
		return fmt.Errorf("database driver instance is unusable after an earlier failure: %w", d.broken)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("database driver %s: %w", call.Op, err)
	}
	call.ReleaseWriters = d.takeReleased()
	input, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("marshal database driver %s: %w", call.Op, err)
	}
	req, err := callRequest(driverwire.Function, input)
	if err != nil {
		return err
	}
	stopInterrupt := context.AfterFunc(ctx, d.inv.close)
	raw, err := d.inst.invoke(withInvocation(ctx, d.inv), req)
	interrupted := !stopInterrupt()
	switch {
	case interrupted || (err != nil && ctx.Err() != nil):
		d.broken = ctx.Err()
		d.inv.close()
		logger.Ctx(ctx).Warn("extension database driver call interrupted, instance discarded",
			zap.String("extension", d.plugin.manifest.Name), zap.String("driver", d.driver),
			zap.String("op", call.Op), zap.Error(ctx.Err()))
		return fmt.Errorf("database driver %s: %w", call.Op, ctx.Err())
	case err != nil && d.plugin.closed.Load():
		d.broken = err
		return fmt.Errorf("database driver %s: plugin closed: %w", call.Op, err)
	case err != nil:
		d.broken = err
		d.inv.close()
		logger.Ctx(ctx).Error("extension database driver fault",
			zap.String("extension", d.plugin.manifest.Name), zap.String("driver", d.driver),
			zap.String("op", call.Op), zap.Error(err))
		return fmt.Errorf("extension database driver fault: driver %q of extension %q failed in %s: %w",
			d.driver, d.plugin.manifest.Name, call.Op, err)
	}

	var reply driverwire.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("parse database driver %s reply: %w", call.Op, err)
	}
	if len(reply.Result) > 0 && out != nil {
		// Numbers stay as written (json.Number): a BIGINT read as float64 would
		// lose digits on its way to the grid, an export or the model.
		dec := json.NewDecoder(bytes.NewReader(reply.Result))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("parse database driver %s result: %w", call.Op, err)
		}
	}
	if reply.Error != nil {
		return &driverError{message: reply.Error.Message, code: reply.Error.Code}
	}
	return nil
}

// driverError is a driver operation's failure, with the driver's own message.
type driverError struct {
	message string
	code    string
}

func (e *driverError) Error() string { return e.message }

// Is lets callers recognize a refusal with errors.Is(err, dbdriver.ErrUnsupported),
// as they do for a built-in driver.
func (e *driverError) Is(target error) bool {
	return target == dbdriver.ErrUnsupported && e.code == driverwire.CodeUnsupported
}

// driverSession is the host's handle on a session: the instances it runs on and
// the operations waiting for one.
type driverSession struct {
	plugin *Plugin
	driver string
	// open is what every instance of the session logs in with — config,
	// decrypted password, reach path — kept for the session's life because
	// instances are logged in on demand: when operations overlap, and after an
	// instance is discarded.
	open dbdriver.OpenRequest
	// reach is the session's reach path as confined for the driver
	// (Plugin.confineReach), shared by every instance.
	reach *dbdriver.Transport
	// tickets is the session's Kerberos ticket gate (Plugin.ticketGate), shared by
	// every instance, so each SPN is logged once per session.
	tickets *ticketGate

	mu sync.Mutex
	// live counts the instances that exist, busy or idle, or are logging in.
	live int
	// idle are the logged-in instances no operation holds, most recently used last.
	idle []*driverInstance
	// queue are the operations waiting for an instance, in arrival order.
	queue  []chan driverGrant
	closed bool
}

var _ dbdriver.Session = (*driverSession)(nil)

// driverGrant is what a waiting operation is handed: an instance, leave to log in
// one of its own (neither set), or the session's closing (err).
type driverGrant struct {
	inst *driverInstance
	err  error
}

// acquire takes an instance for one operation: an idle one, a new one while the
// session has fewer than driverSessionInstances, or else the next one released,
// after the operations that were waiting first. ctx ending while it waits only
// takes the operation out of the queue.
func (s *driverSession) acquire(ctx context.Context) (*driverInstance, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errDriverSessionClosed
	}
	if len(s.queue) == 0 {
		if n := len(s.idle); n > 0 {
			inst := s.idle[n-1]
			s.idle = s.idle[:n-1]
			s.wakeLocked(inst)
			s.mu.Unlock()
			return inst, nil
		}
		if s.live < driverSessionInstances {
			s.live++
			s.mu.Unlock()
			return s.login(ctx)
		}
	}
	grant := make(chan driverGrant, 1)
	s.queue = append(s.queue, grant)
	s.mu.Unlock()

	select {
	case g := <-grant:
		return s.take(ctx, g)
	case <-ctx.Done():
	}
	s.mu.Lock()
	if i := slices.Index(s.queue, grant); i >= 0 {
		s.queue = slices.Delete(s.queue, i, i+1)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
	s.mu.Unlock()
	// Granted as ctx ended: hand the grant on.
	s.forgo(<-grant)
	return nil, ctx.Err()
}

func (s *driverSession) take(ctx context.Context, g driverGrant) (*driverInstance, error) {
	switch {
	case g.err != nil:
		return nil, g.err
	case g.inst != nil:
		return g.inst, nil
	default:
		return s.login(ctx)
	}
}

// forgo passes on a grant its operation no longer wants.
func (s *driverSession) forgo(g driverGrant) {
	switch {
	case g.err != nil:
	case g.inst != nil:
		s.release(g.inst)
	default:
		s.mu.Lock()
		s.live--
		s.grantLocked()
		s.mu.Unlock()
	}
}

// login logs in an instance the session has already counted in live.
func (s *driverSession) login(ctx context.Context) (*driverInstance, error) {
	inst, err := s.plugin.openDriverInstance(ctx, s.driver, &s.open, s.reach, s.tickets)
	if err != nil {
		s.mu.Lock()
		s.live--
		s.grantLocked()
		s.mu.Unlock()
		return nil, err
	}
	return inst, nil
}

// release gives an operation's instance back: to the first waiting operation, or
// to the idle instances. One left unusable, or released after the session
// closed, is closed instead, which makes room for a waiting operation to log in
// one of its own. An instance beyond the session's last one is closed once it
// has stayed idle for the plugin's driver idle timeout.
func (s *driverSession) release(inst *driverInstance) {
	// Asked before taking s.mu: a writer's call may hold the instance a while.
	usable := inst.usable()
	s.mu.Lock()
	if s.closed || !usable {
		s.live--
		s.grantLocked()
		s.mu.Unlock()
		s.retire(inst)
		return
	}
	if len(s.queue) > 0 {
		s.popLocked() <- driverGrant{inst: inst}
		s.mu.Unlock()
		return
	}
	s.idle = append(s.idle, inst)
	inst.idleGen++
	if s.live > 1 {
		gen := inst.idleGen
		inst.idleTimer = time.AfterFunc(s.plugin.opts.driverIdleTimeout, func() { s.reap(inst, gen) })
	}
	s.mu.Unlock()
}

// grantLocked lets the first waiting operation log in an instance of its own
// when the session has room for one. Called with s.mu held whenever an instance
// goes away.
func (s *driverSession) grantLocked() {
	if len(s.queue) == 0 || s.live >= driverSessionInstances {
		return
	}
	s.live++
	s.popLocked() <- driverGrant{}
}

func (s *driverSession) popLocked() chan driverGrant {
	g := s.queue[0]
	s.queue = s.queue[1:]
	return g
}

// wakeLocked ends inst's idle spell.
func (s *driverSession) wakeLocked(inst *driverInstance) {
	inst.idleGen++
	if inst.idleTimer != nil {
		inst.idleTimer.Stop()
		inst.idleTimer = nil
	}
}

// reap closes inst when its idle spell gen has lasted the idle timeout and it is
// not the session's last instance.
func (s *driverSession) reap(inst *driverInstance, gen uint64) {
	s.mu.Lock()
	i := slices.Index(s.idle, inst)
	if s.closed || i < 0 || inst.idleGen != gen || s.live <= 1 {
		s.mu.Unlock()
		return
	}
	s.idle = slices.Delete(s.idle, i, i+1)
	s.wakeLocked(inst)
	s.live--
	s.mu.Unlock()
	s.retire(inst)
}

func (s *driverSession) retire(inst *driverInstance) {
	if err := inst.retire(); err != nil {
		logger.Default().Warn("close extension database driver instance",
			zap.String("extension", s.plugin.manifest.Name), zap.String("driver", s.driver), zap.Error(err))
	}
}

// on runs fn on an instance of the session.
func (s *driverSession) on(ctx context.Context, fn func(*driverInstance) error) error {
	inst, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer s.release(inst)
	return fn(inst)
}

func (s *driverSession) do(ctx context.Context, op string, args driverwire.Args, out any) error {
	return s.on(ctx, func(inst *driverInstance) error {
		return inst.call(ctx, driverwire.Call{Op: op, Session: inst.session, Args: args}, out)
	})
}

// forward runs op and returns its decoded result.
func forward[T any](ctx context.Context, s *driverSession, op string, args driverwire.Args) (T, error) {
	var out T
	err := s.do(ctx, op, args, &out)
	return out, err
}

// forwardRequest runs op with req as its request.
func forwardRequest[T any](ctx context.Context, s *driverSession, op string, args driverwire.Args, req any) (T, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("marshal database driver %s request: %w", op, err)
	}
	args.Request = raw
	return forward[T](ctx, s, op, args)
}

func (s *driverSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, driverwire.OpExecute, driverwire.Args{Statement: statement})
}

func (s *driverSession) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, driverwire.OpExecutePaged, driverwire.Args{Statement: statement, Page: page})
}

func (s *driverSession) ListDatabases(ctx context.Context) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpListDatabases, driverwire.Args{})
}

func (s *driverSession) ListTables(ctx context.Context, database string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpListTables, driverwire.Args{Database: database})
}

func (s *driverSession) TableMetadata(ctx context.Context, database, table string) (*dbdriver.TableMetadata, error) {
	return forward[*dbdriver.TableMetadata](ctx, s, driverwire.OpTableMetadata, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	return forward[*dbdriver.DatabaseObjects](ctx, s, driverwire.OpListObjects, driverwire.Args{Database: database})
}

func (s *driverSession) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	return forward[string](ctx, s, driverwire.OpObjectSource, driverwire.Args{
		Database: database, ObjectType: objType, Name: name, ObjectTable: table,
	})
}

func (s *driverSession) OpenTable(ctx context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	return forward[*dbdriver.OpenedTable](ctx, s, driverwire.OpOpenTable, driverwire.Args{Database: database, Table: table, PageSize: pageSize})
}

func (s *driverSession) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	return forward[*dbdriver.RowSet](ctx, s, driverwire.OpTableRows, driverwire.Args{Database: database, Table: table, Query: query, Page: page})
}

func (s *driverSession) CountTableRows(ctx context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	return forward[int64](ctx, s, driverwire.OpCountTableRows, driverwire.Args{Database: database, Table: table, Filters: filters})
}

// StreamTableRows opens the cursor in the guest and reads it over as many calls
// as it takes, driverStreamRows rows at a time, so the table is never held whole
// on either side. The cursor holds its instance until it is closed. ctx bounds
// every one of those reads, as it would bound reading a database/sql cursor.
func (s *driverSession) StreamTableRows(ctx context.Context, database, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	inst, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var opened driverwire.StreamOpened
	err = inst.call(ctx, driverwire.Call{Op: driverwire.OpStreamTableRows, Session: inst.session, Args: driverwire.Args{
		Database: database, Table: table, Query: query,
	}}, &opened)
	if err != nil {
		s.release(inst)
		return nil, err
	}
	return &driverStream{ctx: ctx, session: s, inst: inst, id: opened.Stream, columns: opened.Columns}, nil
}

func (s *driverSession) TableDDL(ctx context.Context, database, table string) (string, error) {
	return forward[string](ctx, s, driverwire.OpTableDDL, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) StarterQuery(ctx context.Context, database, table string, limit int) (string, error) {
	return forward[string](ctx, s, driverwire.OpStarterQuery, driverwire.Args{Database: database, Table: table, Limit: limit})
}

// PrepareRowStatements prepares the writer in the guest, on one of the session's
// instances. The writer keeps that instance's module alive after the session
// lets go of it, until the writer is collected.
func (s *driverSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	var w *driverRowWriter
	err := s.on(ctx, func(inst *driverInstance) error {
		var prepared driverwire.WriterPrepared
		err := inst.call(ctx, driverwire.Call{Op: driverwire.OpPrepareRowStatements, Session: inst.session, Args: driverwire.Args{
			Database: database, Table: table,
		}}, &prepared)
		if err != nil {
			return err
		}
		inst.hold()
		w = &driverRowWriter{inst: inst, id: prepared.Writer}
		runtime.AddCleanup(w, func(r writerRef) { r.inst.releaseWriter(r.id) }, writerRef{inst: inst, id: prepared.Writer})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *driverSession) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	return forwardRequest[*dbdriver.GridEditStatements](ctx, s, driverwire.OpGridEditStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) CreateTableStatements(ctx context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpCreateTableStatements, driverwire.Args{Database: database}, req)
}

func (s *driverSession) DropTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpDropTableStatements, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) TruncateTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpTruncateTableStatements, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) CreateDatabaseStatements(ctx context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpCreateDatabaseStatements, driverwire.Args{}, req)
}

func (s *driverSession) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	return forward[*dbdriver.AlterTableModel](ctx, s, driverwire.OpAlterTableModel, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	return forwardRequest[*dbdriver.AlterTableStatements](ctx, s, driverwire.OpAlterTableStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpImportStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	return forwardRequest[*dbdriver.BatchResult](ctx, s, driverwire.OpExecuteBatch, driverwire.Args{}, req)
}

// Close closes the session: its idle instances now, side by side — the session in
// the guest, then whatever connection it left open — and each instance still held by an
// operation or a cursor once that lets go of it. Operations waiting for an
// instance fail. Closing twice is a no-op.
func (s *driverSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	idle, queue := s.idle, s.queue
	s.idle, s.queue = nil, nil
	s.live -= len(idle)
	for _, inst := range idle {
		s.wakeLocked(inst)
	}
	s.mu.Unlock()

	for _, g := range queue {
		g <- driverGrant{err: errDriverSessionClosed}
	}
	// Side by side: each close is bounded by the driver close timeout, and a database
	// that no longer answers would otherwise hold the close that long per instance.
	errs := make([]error, len(idle))
	var wg sync.WaitGroup
	for i, inst := range idle {
		wg.Go(func() { errs[i] = inst.retire() })
	}
	wg.Wait()
	return errors.Join(errs...)
}

// driverStream is a cursor in the guest, read over in batches on the instance
// it was opened on, which it holds until closed.
type driverStream struct {
	ctx     context.Context
	session *driverSession
	inst    *driverInstance
	id      uint64
	columns []string

	rows   [][]any
	row    []any
	done   bool
	err    error
	closed bool
}

func (r *driverStream) Columns() []string { return r.columns }

func (r *driverStream) Next() bool {
	for len(r.rows) == 0 {
		if r.done || r.err != nil {
			return false
		}
		var page driverwire.StreamPage
		r.err = r.inst.call(r.ctx, driverwire.Call{Op: driverwire.OpStreamNext, Args: driverwire.Args{
			Stream: r.id, Max: driverStreamRows,
		}}, &page)
		r.rows, r.done = page.Rows, page.Done
	}
	r.row, r.rows = r.rows[0], r.rows[1:]
	return true
}

func (r *driverStream) Row() []any { return r.row }

func (r *driverStream) Err() error { return r.err }

// Close closes the cursor in the guest and gives its instance back to the
// session. A cursor whose instance is already unusable has nothing left to
// close; one whose ctx has ended is closed by discarding its instance, since
// closing it in the guest could mean reading out the rest of the result.
func (r *driverStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	defer r.session.release(r.inst)
	if err := r.ctx.Err(); err != nil {
		r.inst.discard(err)
		return nil
	}
	if !r.inst.usable() {
		return nil
	}
	return r.inst.call(r.ctx, driverwire.Call{Op: driverwire.OpStreamClose, Args: driverwire.Args{Stream: r.id}}, nil)
}

// driverRowWriter is a row statement writer prepared in the guest. Once it is
// collected the guest forgets it (driverInstance.releaseWriter).
type driverRowWriter struct {
	inst *driverInstance
	id   uint64
}

// writerRef is what a writer's cleanup needs: not the writer itself, which would
// keep it reachable.
type writerRef struct {
	inst *driverInstance
	id   uint64
}

func (w *driverRowWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	// The writer stays reachable until its call is done: its cleanup releases it in
	// the guest and its hold on the instance.
	defer runtime.KeepAlive(w)
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal row statements request: %w", err)
	}
	var out []string
	err = w.inst.call(context.Background(), driverwire.Call{Op: driverwire.OpRowStatements, Args: driverwire.Args{
		Writer: w.id, Request: raw,
	}}, &out)
	return out, err
}
