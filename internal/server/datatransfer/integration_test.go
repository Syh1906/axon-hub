package datatransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/db"
	runtimeplugin "github.com/looplj/axonhub/internal/server/plugin"
	"github.com/stretchr/testify/require"
)

// Opt-in: the DSN must name a disposable, local PostgreSQL cluster. Each test
// creates its own databases and deletes only those exact generated names.
func integrationCluster(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	dsn := os.Getenv("AXONHUB_TRANSFER_TEST_DSN")
	if dsn == "" {
		t.Skip("set AXONHUB_TRANSFER_TEST_DSN for native PostgreSQL transfer tests")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname())
	conn, err := pgx.Connect(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn, dsn
}

func testDatabase(t *testing.T, admin *pgx.Conn, dsn string) (string, string) {
	t.Helper()
	name := "axon_transfer_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		require.NoError(t, err)
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	return name, u.String()
}

func testService(t *testing.T, dsn string) *Service {
	t.Helper()
	config := db.Config{Dialect: "postgres", DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 2}
	client := db.NewEntClient(config)
	sys := biz.NewSystemService(biz.SystemServiceParams{Ent: client, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}})
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(systemContext())
	s := &Service{client: client, system: sys, dbConfig: config, config: Config{WorkDir: filepath.Join(dir, "files"), MaxFileGiB: 1, TimeoutSeconds: 60, RetentionHours: 24, DatabaseDiskPath: dir}, ctx: ctx, cancel: cancel}
	t.Cleanup(func() { cancel(); s.wg.Wait(); client.Close() })
	require.NoError(t, s.initializeFiles())
	return s
}

func waitJob(t *testing.T, s *Service) *Job {
	t.Helper()
	var result *Job
	require.Eventually(t, func() bool {
		job, err := s.Job(systemContext())
		if err != nil {
			return false
		}
		result = job
		return job != nil && !job.active()
	}, 30*time.Second, 50*time.Millisecond)
	return result
}

func TestPostgresFullTransferAndDuplicateProtection(t *testing.T) {
	admin, dsn := integrationCluster(t)
	_, sourceDSN := testDatabase(t, admin, dsn)
	_, destinationDSN := testDatabase(t, admin, dsn)
	source := testService(t, sourceDSN)
	destination := testService(t, destinationDSN)
	ctx := systemContext()
	source.client.User.Create().SetEmail("migration@example.invalid").SetPassword("fixture-only").SetFirstName("Migration").SetLastName("Test").SetIsOwner(true).SaveX(ctx)
	source.client.System.Create().SetKey(biz.SystemKeySecretKey).SetValue("source-fixture-secret").SaveX(ctx)
	source.client.System.Create().SetKey(biz.SystemKeyAutoBackupSettings).SetValue(`{"enabled":true}`).SaveX(ctx)
	source.client.System.Create().SetKey("maintenance_cleanup_task_v1").SetValue(`{"status":"running"}`).SaveX(ctx)
	// The rc1 plugin package and its installation-bound storage must survive
	// the same native export/import path as the rest of the deployment.
	var pluginArchive bytes.Buffer
	writer := zip.NewWriter(&pluginArchive)
	for name, body := range map[string]string{
		"manifest.json": `{"schemaVersion":1,"id":"fixture.transfer","name":"Transfer fixture","version":"1.0.0","axonhubVersion":"^1.0.0","pluginApiVersion":1,"ui":{"pages":[{"key":"home","title":"Fixture","entry":"ui/index.html"}]}}`,
		"ui/index.html": "<html><body>transfer fixture</body></html>",
	} {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	parsed, err := runtimeplugin.ParsePackage(pluginArchive.Bytes())
	require.NoError(t, err)
	installed := source.client.Plugin.Create().
		SetPluginID("fixture.transfer").SetName("Transfer fixture").SetVersion("1.0.0").
		SetManifest(parsed.Manifest).SetPackage(pluginArchive.Bytes()).SetPackageSha256(parsed.SHA256).
		SetConfig(map[string]any{"label": "迁移验证"}).SetPriority(17).
		SetGrantedPermissions([]string{"read_channels"}).
		SetGrantedNetwork([]string{"https://fixture.example.invalid"}).SaveX(ctx)
	storageExpiry := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	stored := source.client.PluginStorage.Create().SetInstallationID(installed.ID).
		SetNamespace("transfer").SetKey("state").SetValue(`{"count":7}`).
		SetExpiresAt(storageExpiry).SaveX(ctx)
	conn, err := source.connect(ctx, "")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "CREATE TABLE transfer_fixture (id bigserial PRIMARY KEY, value text NOT NULL); INSERT INTO transfer_fixture(value) SELECT '模型数据-' || i FROM generate_series(1,2000) i")
	require.NoError(t, err)
	conn.Close(ctx)
	id := uuid.NewString()
	_, err = source.StartExport(ctx, id)
	require.NoError(t, err)
	export := waitJob(t, source)
	require.Equal(t, "completed", export.Phase, export.Error)
	require.True(t, export.DownloadAvailable)
	duplicate, err := source.StartExport(ctx, id)
	require.NoError(t, err)
	require.Equal(t, export.UpdatedAt, duplicate.UpdatedAt)
	var archive bytes.Buffer
	require.NoError(t, source.StreamDownload(ctx, id, export.Manifest.SHA256, func(size int64) io.Writer { return &archive }))
	manifestJSON, err := json.Marshal(export.Manifest)
	require.NoError(t, err)
	hash := sha256.Sum256(manifestJSON)
	uploadID := uuid.NewString()
	_, err = destination.InitializeUpload(ctx, uploadID, int64(archive.Len()), hex.EncodeToString(hash[:]))
	require.NoError(t, err)
	_, err = destination.InitializeUpload(ctx, uploadID, int64(archive.Len())+1, hex.EncodeToString(hash[:]))
	require.ErrorIs(t, err, ErrConflict)
	ready, err := destination.Upload(ctx, uploadID, bytes.NewReader(archive.Bytes()))
	require.NoError(t, err)
	require.Equal(t, "ready", ready.Phase)
	target := "axon_transfer_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)")
		require.NoError(t, err)
	})
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("import submission panic: %v", r)
				}
			}()
			_, err := destination.StartImport(ctx, uploadID, target)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			require.ErrorIs(t, err, ErrBusy)
		}
	}
	result := waitJob(t, destination)
	require.Equal(t, "completed", result.Phase, result.Error)
	repeated, err := destination.StartImport(ctx, uploadID, target)
	require.NoError(t, err)
	require.Equal(t, result.UpdatedAt, repeated.UpdatedAt)
	_, err = destination.StartImport(ctx, uploadID, target+"_changed")
	require.ErrorIs(t, err, ErrConflict)
	restored, err := destination.connect(ctx, target)
	require.NoError(t, err)
	defer restored.Close(ctx)
	var count int
	require.NoError(t, restored.QueryRow(ctx, "SELECT count(*) FROM transfer_fixture").Scan(&count))
	require.Equal(t, 2000, count)
	require.NoError(t, restored.QueryRow(ctx, "SELECT count(*) FROM users WHERE email='migration@example.invalid'").Scan(&count))
	require.Equal(t, 1, count)
	var secret string
	require.NoError(t, restored.QueryRow(ctx, "SELECT value FROM systems WHERE key=$1", biz.SystemKeySecretKey).Scan(&secret))
	require.NotEqual(t, "source-fixture-secret", secret)
	require.NoError(t, restored.QueryRow(ctx, "SELECT count(*) FROM systems WHERE key IN ($1,$2)", jobKey, "maintenance_cleanup_task_v1").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, admin.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", roleName(uploadID)).Scan(&count))
	require.Zero(t, count)
	require.Equal(t, "source-fixture-secret", source.client.System.Query().Where(system.KeyEQ(biz.SystemKeySecretKey)).OnlyX(ctx).Value)
	require.Zero(t, destination.client.User.Query().CountX(ctx))
	// The normal destination startup path must still accept the restored schema.
	u, err := url.Parse(destinationDSN)
	require.NoError(t, err)
	u.Path = "/" + target
	upgraded := db.NewEntClient(db.Config{Dialect: "postgres", DSN: u.String(), MaxOpenConns: 2})
	defer upgraded.Close()
	restoredPlugin := upgraded.Plugin.GetX(ctx, installed.ID)
	require.Equal(t, installed.PluginID, restoredPlugin.PluginID)
	require.Equal(t, installed.Version, restoredPlugin.Version)
	require.Equal(t, installed.Manifest, restoredPlugin.Manifest)
	require.Equal(t, installed.Package, restoredPlugin.Package)
	require.Equal(t, installed.PackageSha256, restoredPlugin.PackageSha256)
	require.Equal(t, installed.Config, restoredPlugin.Config)
	require.Equal(t, installed.Priority, restoredPlugin.Priority)
	require.Equal(t, installed.Enabled, restoredPlugin.Enabled)
	require.Equal(t, installed.GrantedPermissions, restoredPlugin.GrantedPermissions)
	require.Equal(t, installed.GrantedNetwork, restoredPlugin.GrantedNetwork)
	restoredStorage := upgraded.PluginStorage.GetX(ctx, stored.ID)
	require.Equal(t, installed.ID, restoredStorage.InstallationID)
	require.Equal(t, stored.Namespace, restoredStorage.Namespace)
	require.Equal(t, stored.Key, restoredStorage.Key)
	require.Equal(t, stored.Value, restoredStorage.Value)
	require.NotNil(t, restoredStorage.ExpiresAt)
	require.True(t, storageExpiry.Equal(*restoredStorage.ExpiresAt))
}

func TestPostgresLostOwnerCannotOverwriteRecovery(t *testing.T) {
	admin, dsn := integrationCluster(t)
	_, dsn = testDatabase(t, admin, dsn)
	s := testService(t, dsn)
	ctx := systemContext()
	lock, err := s.lock(ctx)
	require.NoError(t, err)
	old, created, err := s.newJob(ctx, uuid.NewString(), "export", "full", "exporting")
	require.NoError(t, err)
	require.True(t, created)
	_, err = s.lock(ctx)
	require.ErrorIs(t, err, ErrBusy)
	lock.Close(ctx)
	s.recoverAndExpire()
	current, _, err := s.read(ctx)
	require.NoError(t, err)
	require.Equal(t, "failed", current.Phase)
	old.Phase = "completed"
	require.ErrorIs(t, s.persist(ctx, old), ErrLostOwner)
	_, created, err = s.newJob(ctx, old.ID, "export", "full", "exporting")
	require.NoError(t, err)
	require.False(t, created)
}

func TestPostgresPreparationUsesRestrictedLogin(t *testing.T) {
	admin, dsn := integrationCluster(t)
	_, dsn = testDatabase(t, admin, dsn)
	s := testService(t, dsn)
	ctx := systemContext()
	conn, err := s.connect(ctx, "")
	require.NoError(t, err)
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `CREATE TABLE preparation_identities (role_name text);
CREATE FUNCTION record_preparation_user() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO preparation_identities VALUES(current_user); RETURN NEW; END $$;
CREATE TRIGGER record_preparation BEFORE UPDATE ON systems FOR EACH ROW EXECUTE FUNCTION record_preparation_user();`)
	require.NoError(t, err)
	s.client.System.Create().SetKey(biz.SystemKeySecretKey).SetValue("fixture").SaveX(ctx)
	file, err := os.Create(filepath.Join(t.TempDir(), "source.dump"))
	require.NoError(t, err)
	defer file.Close()
	_, err = s.dump(ctx, file)
	require.NoError(t, err)
	_, err = file.Seek(0, 0)
	require.NoError(t, err)
	job := &Job{ID: uuid.NewString(), TargetDatabase: "axon_transfer_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{job.TargetDatabase}.Sanitize()+" WITH (FORCE)")
		require.NoError(t, err)
	})
	require.NoError(t, s.restore(ctx, job, file))
	target, err := s.connect(ctx, job.TargetDatabase)
	require.NoError(t, err)
	defer target.Close(ctx)
	var actual string
	require.NoError(t, target.QueryRow(ctx, "SELECT role_name FROM preparation_identities LIMIT 1").Scan(&actual))
	require.Equal(t, roleName(job.ID), actual)
}

func TestPostgresExpiredFilesAndPendingRoleCleanup(t *testing.T) {
	admin, dsn := integrationCluster(t)
	_, dsn = testDatabase(t, admin, dsn)
	s := testService(t, dsn)
	ctx := systemContext()
	id := uuid.NewString()
	lock, err := s.lock(ctx)
	require.NoError(t, err)
	job, _, err := s.newJob(ctx, id, "import", "fixture", "failed")
	require.NoError(t, err)
	job.TargetDatabase = "axon_transfer_test_no_database"
	require.NoError(t, s.persist(ctx, job))
	role := pgx.Identifier{roleName(id)}.Sanitize()
	_, err = admin.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN; COMMENT ON ROLE "+role+" IS '"+id+"'")
	require.NoError(t, err)
	lock.Close(ctx)
	s.recoverAndExpire()
	var roles int
	require.NoError(t, admin.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", roleName(id)).Scan(&roles))
	require.Zero(t, roles)
	job, _, err = s.read(ctx)
	require.NoError(t, err)
	job.UpdatedAt = time.Now().Add(-25 * time.Hour)
	require.NoError(t, s.writeFile(job))
	path, err := s.path(id, ".dump")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0600))
	s.recoverAndExpire()
	latest, err := s.Job(ctx)
	require.NoError(t, err)
	require.Equal(t, "expired", latest.Phase)
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	path, err = s.path(id, ".json")
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
}

func TestPostgresUploadStopsWithService(t *testing.T) {
	admin, dsn := integrationCluster(t)
	_, dsn = testDatabase(t, admin, dsn)
	s := testService(t, dsn)
	ctx := systemContext()
	id := uuid.NewString()
	_, err := s.InitializeUpload(ctx, id, 2048, strings.Repeat("a", 64))
	require.NoError(t, err)
	reader, writer := io.Pipe()
	defer writer.Close()
	finished := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("upload panic: %v", r)
			}
		}()
		_, err := s.Upload(ctx, id, reader)
		finished <- err
	}()
	require.Eventually(t, func() bool { job, err := s.Job(ctx); return err == nil && job != nil && job.Phase == "uploading" }, 5*time.Second, 20*time.Millisecond)
	s.cancel()
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("upload outlived service cancellation")
	}
	job, err := s.Job(ctx)
	require.NoError(t, err)
	require.Equal(t, "failed", job.Phase)
}
