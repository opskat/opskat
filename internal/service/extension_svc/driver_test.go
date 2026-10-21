package extension_svc

import (
	"bufio"
	"context"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/assetconn"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo/mock_extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

const (
	fixtureExt    = extensiontest.DBDriverFixtureName
	fixtureDriver = extensiontest.DBDriverFixtureDriver
)

// loginServer is the far end of the fixture driver's reach path: it accepts every
// login and statement announcement (see pkg/extension/testdata/fixture-dbdriver).
type loginServer struct {
	mu     sync.Mutex
	logins int
}

func (s *loginServer) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	go func() {
		defer func() { _ = server.Close() }()
		r := bufio.NewReader(server)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			if _, err := server.Write([]byte("OK\n")); err != nil {
				return
			}
		}
	}()
	return client, nil
}

// driverFixture is a service over an extensions dir holding the database driver
// fixture, with mocked repositories.
type driverFixture struct {
	svc     *Service
	dir     string
	assets  *mock_asset_repo.MockAssetRepo
	state   *mock_extension_state_repo.MockExtensionStateRepo
	reloads int
	server  *loginServer
}

func newDriverFixture(t *testing.T) *driverFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &driverFixture{
		dir:    t.TempDir(),
		assets: mock_asset_repo.NewMockAssetRepo(ctrl),
		state:  mock_extension_state_repo.NewMockExtensionStateRepo(ctrl),
		server: &loginServer{},
	}
	extensiontest.InstallDBDriverFixture(t, f.dir)
	f.state.EXPECT().Find(gomock.Any(), gomock.Any()).Return(nil, gorm.ErrRecordNotFound).AnyTimes()
	f.state.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	f.state.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	f.svc = New(newTestManager(f.dir), f.state, mock_extension_data_repo.NewMockExtensionDataRepo(ctrl), f.assets,
		zap.NewNop(), func() { f.reloads++ }, nil)
	t.Cleanup(func() {
		for _, name := range f.svc.Bridge().ListNames() {
			extreg.Unregister(name)
		}
		f.svc.Close(context.Background())
	})
	return f
}

// init starts the service with the fixture enabled or disabled.
func (f *driverFixture) init(t *testing.T, enabled bool) {
	t.Helper()
	var states []*extension_state_entity.ExtensionState
	if !enabled {
		states = append(states, &extension_state_entity.ExtensionState{Name: fixtureExt, Enabled: false})
	}
	f.state.EXPECT().FindAll(gomock.Any()).Return(states, nil)
	require.NoError(t, f.svc.Init(context.Background()))
}

// databaseAssets makes the asset repo report ids as the database assets using the fixture's driver.
func (f *driverFixture) databaseAssets(ids ...int64) {
	assets := make([]*asset_entity.Asset, 0, len(ids))
	for _, id := range ids {
		assets = append(assets, &asset_entity.Asset{ID: id, Type: asset_entity.AssetTypeDatabase})
	}
	f.assets.EXPECT().List(gomock.Any(), asset_repo.ListOptions{DatabaseDriver: fixtureDriver}).Return(assets, nil).AnyTimes()
}

// open opens a session of the fixture's driver the way connpool does: through the registry.
func (f *driverFixture) open(t *testing.T) dbdriver.Session {
	t.Helper()
	d, err := dbregistry.Lookup(fixtureDriver)
	require.NoError(t, err)
	s, err := d.Open(context.Background(), dbdriver.OpenRequest{
		Config:    &dbdriver.Config{Driver: fixtureDriver, Host: "db.internal", Port: 7357, Username: "app"},
		Password:  "pw",
		Transport: dbdriver.Transport{Dial: f.server.dial},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// invalidated records the assets whose cached connections were dropped.
func invalidated(t *testing.T) func() []int64 {
	t.Helper()
	var mu sync.Mutex
	var ids []int64
	assetconn.RegisterInvalidator("driver-test", func(_ context.Context, id int64) error {
		mu.Lock()
		defer mu.Unlock()
		ids = append(ids, id)
		return nil
	})
	t.Cleanup(func() { assetconn.UnregisterForTest("driver-test") })
	return func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(ids)
	}
}

func requireDriverUnavailable(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), `"`+fixtureDriver+`"`)
}

func TestDisablingADriverExtensionClosesItsSessionsAndEnablingRestoresIt(t *testing.T) {
	f := newDriverFixture(t)
	f.init(t, true)
	f.databaseAssets(11, 12)
	dropped := invalidated(t)
	f.state.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	session := f.open(t)
	_, err := session.ListDatabases(context.Background())
	require.NoError(t, err)

	require.NoError(t, f.svc.Disable(context.Background(), fixtureExt), "disabling is not refused while assets use the driver")

	_, err = session.ListDatabases(context.Background())
	requireDriverUnavailable(t, err)
	_, err = dbregistry.Lookup(fixtureDriver)
	requireDriverUnavailable(t, err)
	assert.ElementsMatch(t, []int64{11, 12}, dropped(), "the assets' cached sessions are dropped")
	assert.Equal(t, 1, f.reloads, "the frontend is told the extensions changed")

	require.NoError(t, f.svc.Enable(context.Background(), fixtureExt))
	_, err = f.open(t).ListDatabases(context.Background())
	require.NoError(t, err, "the same driver id connects again without editing the asset")
}

func TestUpgradingADriverExtensionClosesTheOldVersionsSessions(t *testing.T) {
	f := newDriverFixture(t)
	f.init(t, true)
	f.databaseAssets(11)
	dropped := invalidated(t)

	old := f.open(t)
	newVersion := extensiontest.InstallDBDriverFixture(t, t.TempDir())
	_, err := f.svc.Install(context.Background(), newVersion, nil)
	require.NoError(t, err)

	_, err = old.ListDatabases(context.Background())
	requireDriverUnavailable(t, err)
	assert.Equal(t, []int64{11}, dropped())
	_, err = f.open(t).ListDatabases(context.Background())
	require.NoError(t, err, "the next operation opens a session of the new version")
}

func TestUninstallIsRefusedWhileDatabaseAssetsUseItsDriver(t *testing.T) {
	for name, enabled := range map[string]bool{"enabled": true, "disabled": false} {
		t.Run(name, func(t *testing.T) {
			f := newDriverFixture(t)
			f.init(t, enabled)
			f.databaseAssets(11, 12)

			err := f.svc.Uninstall(context.Background(), fixtureExt, false, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "2 database asset(s)")
			assert.Contains(t, err.Error(), `"`+fixtureDriver+`"`)
			assert.FileExists(t, filepath.Join(f.dir, fixtureExt, "manifest.json"), "nothing is removed")
			if enabled {
				_, err = dbregistry.Lookup(fixtureDriver)
				assert.NoError(t, err, "the driver stays registered")
			}

			require.NoError(t, f.svc.Uninstall(context.Background(), fixtureExt, false, true), "a forced uninstall goes ahead")
			assert.NoFileExists(t, filepath.Join(f.dir, fixtureExt, "manifest.json"))
			_, err = dbregistry.Lookup(fixtureDriver)
			requireDriverUnavailable(t, err)
		})
	}
}

func TestUninstallGoesAheadWhenNoAssetUsesItsDriver(t *testing.T) {
	f := newDriverFixture(t)
	f.init(t, true)
	f.databaseAssets()

	require.NoError(t, f.svc.Uninstall(context.Background(), fixtureExt, false, false))
	_, err := dbregistry.Lookup(fixtureDriver)
	requireDriverUnavailable(t, err)
}

// The extension that provides a database asset's driver reaches that asset only
// through a session of its driver: the paths that hand an extension's own assets to
// it — config, credentials, form, connection path — refuse it, naming the extension.
func TestDatabaseAssetReachesItsDriverExtensionOnlyThroughTheSession(t *testing.T) {
	svc, assets := newHostTestService(t)
	svc.Bridge().Register(&extension.Extension{Name: "mydb", Manifest: &extension.Manifest{
		Name: "mydb", Capabilities: extension.Capabilities{DatabaseDrivers: []string{"mydb"}},
	}})
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{
		ID: 1, Name: "db", Type: asset_entity.AssetTypeDatabase,
		Config: `{"driver":"mydb","host":"h","port":1,"username":"u","password":"ciphertext"}`,
	}, nil).AnyTimes()
	ctx := context.Background()

	const refused = `does not belong to extension "mydb"`
	_, err := configGetterOf(svc, "mydb").GetAssetConfig(1)
	require.ErrorContains(t, err, refused)
	_, err = configGetterOf(svc, "mydb").AssetCredentialValues(ctx, 1, []string{"password"})
	require.ErrorContains(t, err, refused)
	_, err = svc.FormConfig(ctx, "mydb", 1)
	require.ErrorContains(t, err, refused)
	_, _, _, err = dialerOf(svc, "mydb").DialContextFor(ctx, 1)
	require.ErrorContains(t, err, refused)
}

// The driver check reads the manifest of an installed extension; a name that is
// not installed has no drivers to check, and uninstalling it stays the no-op it
// was, rather than a refusal that tells the user to force it.
func TestUninstallOfAnExtensionThatIsNotInstalledHasNoDriversToCheck(t *testing.T) {
	f := newDriverFixture(t)
	f.init(t, true)

	require.NoError(t, f.svc.Uninstall(context.Background(), "never-installed", false, false))
}
