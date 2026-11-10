package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	entplugin "github.com/looplj/axonhub/internal/ent/plugin"
	"github.com/looplj/axonhub/internal/ent/pluginstorage"
	runtimeplugin "github.com/looplj/axonhub/internal/server/plugin"
)

func TestPluginBackupRestore(t *testing.T) {
	source, svc, ctx := setupBackupTest(t)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	manifest := `{"schemaVersion":1,"id":"fixture.backup","name":"Backup fixture","version":"1.0.0","axonhubVersion":"*","pluginApiVersion":1,"ui":{"pages":[{"key":"home","title":{"en":"Fixture","zh-CN":"验收"},"entry":"ui/index.html"}]}}`
	for name, body := range map[string]string{"manifest.json": manifest, "ui/index.html": "<html><body>fixture</body></html>"} {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	parsed, err := runtimeplugin.ParsePackage(archive.Bytes())
	require.NoError(t, err)
	row := source.Plugin.Create().SetPluginID(parsed.Manifest.ID).SetName("Backup fixture").SetVersion("1.0.0").SetManifest(parsed.Manifest).SetPackage(archive.Bytes()).SetPackageSha256(parsed.SHA256).SetEnabled(true).SetGrantedPermissions([]string{"read_requests"}).SetConfig(map[string]any{"note": "fixture"}).SaveX(ctx)
	source.PluginStorage.Create().SetInstallationID(row.ID).SetNamespace("notes").SetKey("entry").SetValue("retained").SaveX(ctx)
	data, err := svc.Backup(ctx, BackupOptions{IncludeSystemConfigs: true})
	require.NoError(t, err)
	var snapshot BackupData
	require.NoError(t, json.Unmarshal(data, &snapshot))
	require.Len(t, snapshot.Plugins, 1)
	require.Equal(t, archive.Bytes(), snapshot.Plugins[0].Package)
	require.NoError(t, source.Close())
	target, restore, targetCtx := setupBackupTest(t)
	defer target.Close()
	existingManifest := parsed.Manifest
	existingManifest.ID = "fixture.existing"
	target.Plugin.Create().SetPluginID(existingManifest.ID).SetManifest(existingManifest).SaveX(targetCtx)
	options := RestoreOptions{IncludeSystemConfigs: true}
	require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), options))
	got := target.Plugin.Query().Where(entplugin.PluginIDEQ("fixture.backup")).OnlyX(targetCtx)
	require.NotEqual(t, row.ID, got.ID)
	require.False(t, got.Enabled)
	require.Empty(t, got.GrantedPermissions)
	require.Empty(t, got.GrantedNetwork)
	require.Equal(t, "fixture", got.Config["note"])
	stored := target.PluginStorage.Query().Where(pluginstorage.InstallationIDEQ(got.ID)).OnlyX(targetCtx)
	require.Equal(t, "retained", stored.Value)
	require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), options))
	require.Equal(t, 1, target.PluginStorage.Query().CountX(targetCtx))
	// Previous-format backups remain readable and do not alter current plugins.
	snapshot.Version = BackupVersionV8
	snapshot.Plugins = nil
	snapshot.PluginStorage = nil
	old, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(old), options))
	require.Equal(t, 2, target.Plugin.Query().CountX(targetCtx))
}
