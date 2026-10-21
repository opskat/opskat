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
// config, the decrypted password, the driver options and the reach path, which
// every TCP connection the driver opens takes — each time an instance of the
// session logs in. A driver that cannot connect fails with its own error.
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
	s := &driverSession{plugin: p, driver: id, open: req, live: 1}
	inst, err := s.login(ctx)
	if err != nil {
		return nil, err
	}
	s.release(inst)
	return s, nil
}

// openDriverInstance creates an instance and logs it in: the driver opens its
// session in the guest and connects.
func (p *Plugin) openDriverInstance(ctx context.Context, driver string, req *dbdriver.OpenRequest) (*driverInstance, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("plugin closed")
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		return nil, err
	}
	inv := newInvocation(p.nextInvocationID(), nil)
	inv.reach = p.confineReach(driver, req)
	d := &driverInstance{plugin: p, driver: driver, inst: inst, inv: inv}
	d.refs.Store(1)

	var opened driverwire.Opened
	err = d.call(ctx, driverwire.Call{Op: driverwire.OpOpen, Args: driverwire.Args{
		Driver: dbdriver.DriverID(driver), Config: req.Config, Password: req.Password, DriverOptions: req.DriverOptions,
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

// confineReach is the reach path an instance's invocation dials through: the
// session's own, narrowed to the one address the asset's config names. The host
// resolved that path for that address alone (tunnel, proxy chain or direct), so a
// driver gets no way to reach anything else along it. A refused address is named
// in the error the guest gets and logged; the log carries no credential, only
// the addresses. A session without a Dial of its own dials directly, as a TCP
// stream of any other host call does.
func (p *Plugin) confineReach(driver string, req *dbdriver.OpenRequest) *dbdriver.Transport {
	transport := req.Transport
	allowed := ""
	if cfg := req.Config; cfg != nil {
		allowed = net.JoinHostPort(canonicalHost(cfg.Host), strconv.Itoa(cfg.Port))
	}
	dial := transport.Dial
	if dial == nil {
		dial = (&netdial.Dialer{}).DialContext
	}
	transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !sameEndpoint(allowed, addr) {
			logger.Ctx(ctx).Warn("extension database driver dial denied",
				zap.String("extension", p.manifest.Name), zap.String("driver", driver),
				zap.String("addr", addr), zap.String("allowed", allowed))
			return nil, fmt.Errorf("dial denied: %q is not the address of the database asset (%s)", addr, allowed)
		}
		return dial(ctx, network, addr)
	}
	return &transport
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
	inst, err := s.plugin.openDriverInstance(ctx, s.driver, &s.open)
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
