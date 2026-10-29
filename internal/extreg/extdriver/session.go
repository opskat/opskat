// Package extdriver runs the database drivers extensions provide: a driver's
// session lives in the guest (pkg/extsdk/sdkdbdriver DatabaseDriver assembles it
// there), and the host holds a dbdriver.Session that forwards every operation
// across the boundary as JSON (pkg/extwire). To shared code it is a session like
// any built-in driver's. The extension runtime (pkg/extension) knows nothing of
// databases: a session runs on instances it leases (Plugin.Lease), each bound to
// the scope this package builds from the open request.
package extdriver

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
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// A session runs on up to driverSessionInstances module instances of its own,
// leased outside the plugin's call pool. Each instance logs in once — its own
// session in the guest, its own database connection — and keeps that session,
// its connection and its cursors in its memory between calls; its invocation,
// with the IO handle table that holds the connection, lasts as long as the
// instance instead of one call. A WASM instance runs one call at a time, so an
// operation holds an instance for as long as it runs: operations beyond the
// instances run on a new one, logged in on demand, or — once the session has all
// of them — wait for one in arrival order. A cursor holds its instance from open
// to close.
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
	// defaultIdleTimeout is how long an instance of a session may stay idle
	// before it is closed, unless it is the session's last one.
	defaultIdleTimeout = time.Minute
	// defaultCloseTimeout bounds closing an instance's session in the guest: a
	// driver's close may talk to a database that no longer answers.
	defaultCloseTimeout = 10 * time.Second
)

// errDriverSessionClosed is what an operation on a closed session fails with.
var errDriverSessionClosed = errors.New("database driver session is closed")

// Drivers are the database drivers a loaded extension provides: the ones its
// manifest declares in contributes.databaseDrivers.
type Drivers struct {
	plugin *extension.Plugin
	// idleTimeout is defaultIdleTimeout; tests shorten it.
	idleTimeout time.Duration
	// closeTimeout is defaultCloseTimeout; tests shorten it.
	closeTimeout time.Duration
}

// New returns the database drivers plugin provides.
func New(plugin *extension.Plugin) *Drivers {
	return &Drivers{plugin: plugin, idleTimeout: defaultIdleTimeout, closeTimeout: defaultCloseTimeout}
}

func (d *Drivers) name() string { return d.plugin.Manifest().Name }

// provides checks the extension declares driver id.
func (d *Drivers) provides(id string) error {
	if !slices.Contains(d.plugin.Manifest().Contributes.DatabaseDrivers, id) {
		return fmt.Errorf("extension %q does not provide database driver %q", d.name(), id)
	}
	return nil
}

// OpenDriverSession opens a session of database driver id, which the extension
// must declare in contributes.databaseDrivers. req is handed to the driver — its
// config, the decrypted password, the driver options and the TLS material — each
// time an instance of the session logs in; its reach path and Kerberos ticket
// source become the session's scope (driverScope), which every host call of the
// driver is admitted against with the extension's permissions. A driver that
// cannot connect fails with its own error.
//
// The session's calls are bounded only by ctx — the asset's query timeout and
// the caller's cancellation — not by the plugin's tool timeout.
func (d *Drivers) OpenDriverSession(ctx context.Context, id string, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if err := d.provides(id); err != nil {
		return nil, err
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
	s := &driverSession{drivers: d, driver: id, open: req, live: 1}
	s.scope = d.driverScope(id, &s.open)
	inst, err := s.login(ctx)
	if err != nil {
		return nil, err
	}
	s.release(inst)
	return s, nil
}

// ClassifyStatements splits and classifies sql with database driver id
// (dbdriver.StatementClassifier), which the extension must declare in
// contributes.databaseDrivers. No session is involved: the driver does not touch
// the database to answer, so the call runs on a pooled instance like
// CheckPolicy, with no asset, no reach path and the plugin's tool timeout
// (Plugin.Invoke). The statements are the driver's as it answered them;
// dbdriver.ClassifyStatements checks them. A failure the driver answers — text
// it cannot split — wraps dbdriver.ErrCannotSplit with the driver's own reason.
func (d *Drivers) ClassifyStatements(ctx context.Context, id, sql string) ([]dbdriver.Statement, error) {
	if err := d.provides(id); err != nil {
		return nil, err
	}
	input, err := json.Marshal(extwire.DriverCall{Op: extwire.DriverOpClassifyStatements, Args: extwire.DriverArgs{
		Driver: dbdriver.DriverID(id), Statement: sql,
	}})
	if err != nil {
		return nil, fmt.Errorf("marshal database driver %s: %w", extwire.DriverOpClassifyStatements, err)
	}
	raw, err := d.plugin.Invoke(ctx, extwire.DriverFunction, input)
	if err != nil {
		return nil, err
	}
	var reply extwire.DriverReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("parse database driver %s reply: %w", extwire.DriverOpClassifyStatements, err)
	}
	if reply.Error != nil {
		return nil, fmt.Errorf("%w: %w", dbdriver.ErrCannotSplit, &driverError{message: reply.Error.Message, code: reply.Error.Code})
	}
	var stmts []dbdriver.Statement
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &stmts); err != nil {
			return nil, fmt.Errorf("parse database driver %s result: %w", extwire.DriverOpClassifyStatements, err)
		}
	}
	return stmts, nil
}

// openInstance leases an instance and logs it in: the driver opens its session
// in the guest and connects, every host call of the instance admitted against
// the session's scope (driverScope).
func (s *driverSession) openInstance(ctx context.Context) (*driverInstance, error) {
	inst, err := s.drivers.plugin.Lease(ctx, s.scope)
	if err != nil {
		return nil, err
	}
	d := &driverInstance{drivers: s.drivers, driver: s.driver, inst: inst}
	d.refs.Store(1)

	req := &s.open
	var opened extwire.DriverOpened
	err = d.call(ctx, extwire.DriverCall{Op: extwire.DriverOpOpen, Args: extwire.DriverArgs{
		Driver: dbdriver.DriverID(s.driver), Config: req.Config, Password: req.Password, DriverOptions: req.DriverOptions,
		TLS: req.TLS, Kerberos: req.Kerberos != nil,
	}}, &opened)
	if err != nil {
		d.inst.Interrupt()
		d.drop()
		logger.Ctx(ctx).Warn("extension database driver instance login failed",
			zap.String("extension", s.drivers.name()), zap.String("driver", s.driver), zap.String("invocation", inst.ID()), zap.Error(err))
		return nil, err
	}
	d.session = opened.Session
	logger.Ctx(ctx).Info("extension database driver instance logged in",
		zap.String("extension", s.drivers.name()), zap.String("driver", s.driver), zap.String("invocation", inst.ID()))
	return d, nil
}

// driverScopeOps are the host calls a driver call may make: logging, the TCP
// streams (with their deadlines) that carry its connection to the database, the
// stop check, and the Kerberos tickets it logs in with. Everything else (KV,
// file dialogs, asset config, tool events, HTTP, files) is refused.
var driverScopeOps = []string{
	extension.OpLog, extension.IOOpenOp("tcp"), extension.OpIOSetDeadline, extension.OpToolShouldStop, extension.OpKerberosTicket,
}

// driverScope is the scope every instance of a session runs in, built from the
// open request the database side assembled: the asset's host:port as its one
// endpoint, the request's reach path as its route, its Kerberos ticket source,
// and the fixed driver ops. The asset's own address takes the transport's Dial,
// as a built-in driver's connection does; any other address — a redirect the
// database answered with, reachable with network.assetRoute — goes by address
// (Transport.DialAddr). A transport without a by-address dial dials directly and
// gets the direct route's check whatever Direct says.
func (d *Drivers) driverScope(driver string, req *dbdriver.OpenRequest) *extension.Scope {
	scope := &extension.Scope{Asset: &extension.AssetRef{ID: req.AssetID}, Ops: driverScopeOps}
	if cfg := req.Config; cfg != nil {
		scope.Endpoints = []extension.Endpoint{{Host: cfg.Host, Port: strconv.Itoa(cfg.Port)}}
	}
	dial, dialAddr := orDirectDial(req.Transport.Dial), orDirectDial(req.Transport.DialAddr)
	scope.Route = &extension.Route{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if scope.HasEndpoint(addr) {
				return dial(ctx, network, addr)
			}
			return dialAddr(ctx, network, addr)
		},
		Direct: req.Transport.Direct || req.Transport.DialAddr == nil,
	}
	if req.Kerberos != nil {
		scope.Tickets = &driverTickets{
			extension: d.name(), driver: driver, assetID: req.AssetID,
			source: req.Kerberos, issued: map[string]bool{},
		}
	}
	return scope
}

func orDirectDial(dial dbdriver.DialFunc) extension.DialContextFunc {
	if dial == nil {
		return (&netdial.Dialer{}).DialContext
	}
	return extension.DialContextFunc(dial)
}

// driverTickets is a session's Kerberos ticket source: the one the caller of
// OpenDriverSession supplied. The guest only names the service principal; the
// source refuses one that is not service/host or that differs from the asset's
// pinned one, and logs the session's first ticket for each — the principal and
// SPN only, never a credential or a ticket.
type driverTickets struct {
	extension, driver string
	assetID           int64
	source            *dbdriver.Kerberos

	mu sync.Mutex
	// issued are the SPNs the session has been given a ticket for, logged on the
	// first of each.
	issued map[string]bool
}

func (g *driverTickets) Ticket(ctx context.Context, spn string) ([]byte, error) {
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

// driverInstance is one leased instance of a session, logged in to the database.
// Calls into it are serialized: an instance runs one call at a time.
//
// It is reference counted because a prepared row statement writer outlives its
// session (dbdriver.Session.PrepareRowStatements) and generates in the guest: the
// instance is closed once the session has let go of it and every writer
// prepared on it has been collected.
type driverInstance struct {
	drivers *Drivers
	driver  string
	inst    *extension.Instance
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

// drop lets go of one hold; the last closes the instance. No call can be running
// then: calls are made by the session, which holds the instance until it retires
// it, and by writers, which hold it while they are reachable.
func (d *driverInstance) drop() {
	if d.refs.Add(-1) > 0 {
		return
	}
	if err := d.inst.Close(context.Background()); err != nil {
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
	d.inst.Interrupt()
}

// retire closes the instance's session in the guest — when the instance is
// still usable — and its connections, and lets go of the instance; a writer
// prepared on it keeps the instance until the writer is collected. Closing in
// the guest is bounded by the driver close timeout: a driver's close may wait on
// a database that no longer answers, and retiring runs when an extension is
// disabled or removed and when an idle instance is reaped.
func (d *driverInstance) retire() error {
	var err error
	if d.usable() {
		ctx, cancel := context.WithTimeout(context.Background(), d.drivers.closeTimeout)
		err = d.call(ctx, extwire.DriverCall{Op: extwire.DriverOpClose, Session: d.session}, nil)
		cancel()
	}
	d.inst.Interrupt()
	d.drop()
	logger.Default().Info("extension database driver instance closed",
		zap.String("extension", d.drivers.name()), zap.String("driver", d.driver), zap.String("invocation", d.inst.ID()))
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
func (d *driverInstance) call(ctx context.Context, call extwire.DriverCall, out any) error {
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
	raw, err := d.inst.Invoke(ctx, extwire.DriverFunction, input)
	switch {
	case err != nil && ctx.Err() != nil:
		d.broken = ctx.Err()
		d.inst.Interrupt()
		logger.Ctx(ctx).Warn("extension database driver call interrupted, instance discarded",
			zap.String("extension", d.drivers.name()), zap.String("driver", d.driver),
			zap.String("op", call.Op), zap.Error(ctx.Err()))
		return fmt.Errorf("database driver %s: %w", call.Op, ctx.Err())
	case errors.Is(err, extension.ErrPluginClosed):
		d.broken = err
		return fmt.Errorf("database driver %s: %w", call.Op, err)
	case err != nil:
		d.broken = err
		d.inst.Interrupt()
		logger.Ctx(ctx).Error("extension database driver fault",
			zap.String("extension", d.drivers.name()), zap.String("driver", d.driver),
			zap.String("op", call.Op), zap.Error(err))
		return fmt.Errorf("extension database driver fault: driver %q of extension %q failed in %s: %w",
			d.driver, d.drivers.name(), call.Op, err)
	}

	var reply extwire.DriverReply
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
	return target == dbdriver.ErrUnsupported && e.code == extwire.DriverCodeUnsupported
}

// driverSession is the host's handle on a session: the instances it runs on and
// the operations waiting for one.
type driverSession struct {
	drivers *Drivers
	driver  string
	// open is what every instance of the session logs in with — config,
	// decrypted password, reach path — kept for the session's life because
	// instances are logged in on demand: when operations overlap, and after an
	// instance is discarded.
	open dbdriver.OpenRequest
	// scope is what every instance of the session may reach (driverScope),
	// shared by every instance, so each redirect address and each SPN is logged
	// once per session.
	scope *extension.Scope

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
	inst, err := s.openInstance(ctx)
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
// has stayed idle for the driver idle timeout (Drivers).
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
		inst.idleTimer = time.AfterFunc(s.drivers.idleTimeout, func() { s.reap(inst, gen) })
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
			zap.String("extension", s.drivers.name()), zap.String("driver", s.driver), zap.Error(err))
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

func (s *driverSession) do(ctx context.Context, op string, args extwire.DriverArgs, out any) error {
	return s.on(ctx, func(inst *driverInstance) error {
		return inst.call(ctx, extwire.DriverCall{Op: op, Session: inst.session, Args: args}, out)
	})
}

// forward runs op and returns its decoded result.
func forward[T any](ctx context.Context, s *driverSession, op string, args extwire.DriverArgs) (T, error) {
	var out T
	err := s.do(ctx, op, args, &out)
	return out, err
}

// forwardRequest runs op with req as its request.
func forwardRequest[T any](ctx context.Context, s *driverSession, op string, args extwire.DriverArgs, req any) (T, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("marshal database driver %s request: %w", op, err)
	}
	args.Request = raw
	return forward[T](ctx, s, op, args)
}

func (s *driverSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, extwire.DriverOpExecute, extwire.DriverArgs{Statement: statement})
}

func (s *driverSession) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, extwire.DriverOpExecutePaged, extwire.DriverArgs{Statement: statement, Page: page})
}

func (s *driverSession) ListDatabases(ctx context.Context) ([]string, error) {
	return forward[[]string](ctx, s, extwire.DriverOpListDatabases, extwire.DriverArgs{})
}

func (s *driverSession) ListTables(ctx context.Context, database string) ([]string, error) {
	return forward[[]string](ctx, s, extwire.DriverOpListTables, extwire.DriverArgs{Database: database})
}

func (s *driverSession) TableMetadata(ctx context.Context, database, table string) (*dbdriver.TableMetadata, error) {
	return forward[*dbdriver.TableMetadata](ctx, s, extwire.DriverOpTableMetadata, extwire.DriverArgs{Database: database, Table: table})
}

func (s *driverSession) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	return forward[*dbdriver.DatabaseObjects](ctx, s, extwire.DriverOpListObjects, extwire.DriverArgs{Database: database})
}

func (s *driverSession) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	return forward[string](ctx, s, extwire.DriverOpObjectSource, extwire.DriverArgs{
		Database: database, ObjectType: objType, Name: name, ObjectTable: table,
	})
}

func (s *driverSession) OpenTable(ctx context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	return forward[*dbdriver.OpenedTable](ctx, s, extwire.DriverOpOpenTable, extwire.DriverArgs{Database: database, Table: table, PageSize: pageSize})
}

func (s *driverSession) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	return forward[*dbdriver.RowSet](ctx, s, extwire.DriverOpTableRows, extwire.DriverArgs{Database: database, Table: table, Query: query, Page: page})
}

func (s *driverSession) CountTableRows(ctx context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	return forward[int64](ctx, s, extwire.DriverOpCountTableRows, extwire.DriverArgs{Database: database, Table: table, Filters: filters})
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
	var opened extwire.DriverStreamOpened
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpStreamTableRows, Session: inst.session, Args: extwire.DriverArgs{
		Database: database, Table: table, Query: query,
	}}, &opened)
	if err != nil {
		s.release(inst)
		return nil, err
	}
	return &driverStream{ctx: ctx, session: s, inst: inst, id: opened.Stream, columns: opened.Columns}, nil
}

func (s *driverSession) TableDDL(ctx context.Context, database, table string) (string, error) {
	return forward[string](ctx, s, extwire.DriverOpTableDDL, extwire.DriverArgs{Database: database, Table: table})
}

func (s *driverSession) StarterQuery(ctx context.Context, database, table string, limit int) (string, error) {
	return forward[string](ctx, s, extwire.DriverOpStarterQuery, extwire.DriverArgs{Database: database, Table: table, Limit: limit})
}

// PrepareRowStatements prepares the writer in the guest, on one of the session's
// instances. The writer keeps that instance's module alive after the session
// lets go of it, until the writer is collected.
func (s *driverSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	var w *driverRowWriter
	err := s.on(ctx, func(inst *driverInstance) error {
		var prepared extwire.DriverWriterPrepared
		err := inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpPrepareRowStatements, Session: inst.session, Args: extwire.DriverArgs{
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
	return forwardRequest[*dbdriver.GridEditStatements](ctx, s, extwire.DriverOpGridEditStatements, extwire.DriverArgs{Database: database, Table: table}, req)
}

func (s *driverSession) CreateTableStatements(ctx context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, extwire.DriverOpCreateTableStatements, extwire.DriverArgs{Database: database}, req)
}

func (s *driverSession) DropTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, extwire.DriverOpDropTableStatements, extwire.DriverArgs{Database: database, Table: table})
}

func (s *driverSession) TruncateTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, extwire.DriverOpTruncateTableStatements, extwire.DriverArgs{Database: database, Table: table})
}

func (s *driverSession) CreateDatabaseStatements(ctx context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, extwire.DriverOpCreateDatabaseStatements, extwire.DriverArgs{}, req)
}

func (s *driverSession) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	return forward[*dbdriver.AlterTableModel](ctx, s, extwire.DriverOpAlterTableModel, extwire.DriverArgs{Database: database, Table: table})
}

func (s *driverSession) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	return forwardRequest[*dbdriver.AlterTableStatements](ctx, s, extwire.DriverOpAlterTableStatements, extwire.DriverArgs{Database: database, Table: table}, req)
}

func (s *driverSession) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, extwire.DriverOpImportStatements, extwire.DriverArgs{Database: database, Table: table}, req)
}

func (s *driverSession) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	return forwardRequest[*dbdriver.BatchResult](ctx, s, extwire.DriverOpExecuteBatch, extwire.DriverArgs{}, req)
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
		var page extwire.DriverStreamPage
		r.err = r.inst.call(r.ctx, extwire.DriverCall{Op: extwire.DriverOpStreamNext, Args: extwire.DriverArgs{
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
	return r.inst.call(r.ctx, extwire.DriverCall{Op: extwire.DriverOpStreamClose, Args: extwire.DriverArgs{Stream: r.id}}, nil)
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
	err = w.inst.call(context.Background(), extwire.DriverCall{Op: extwire.DriverOpRowStatements, Args: extwire.DriverArgs{
		Writer: w.id, Request: raw,
	}}, &out)
	return out, err
}
