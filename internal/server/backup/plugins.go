package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	entplugin "github.com/looplj/axonhub/internal/ent/plugin"
	"github.com/looplj/axonhub/internal/ent/pluginstorage"
	runtimeplugin "github.com/looplj/axonhub/internal/server/plugin"
)

// Packages are bounded at 5 MiB each; export one at a time so a large plugin
// installation set cannot turn the streaming backup into a full-memory query.
func (svc *BackupService) streamPlugins(ctx context.Context, o *objWriter, opts BackupOptions) error {
	if !opts.IncludeSystemConfigs {
		return nil
	}
	_, err := authz.RunAsHost(ctx, svc.db, "backup-plugins", func(hostCtx context.Context) (struct{}, error) {
		ids := make([]int, 0)
		err := streamArrayField(o, "plugins", true, true, func(lastID int) ([]*ent.Plugin, int, error) {
			rows, err := svc.db.Plugin.Query().Where(entplugin.IDGT(lastID)).Order(ent.Asc(entplugin.FieldID)).Limit(1).All(hostCtx)
			next := 0
			if len(rows) > 0 {
				next = rows[len(rows)-1].ID
				ids = append(ids, next)
			}
			return rows, next, err
		}, func(row *ent.Plugin) ([]byte, bool, error) { data, err := json.Marshal(row); return data, true, err })
		if err != nil {
			return struct{}{}, err
		}
		err = streamArrayField(o, "plugin_storage", true, true, func(lastID int) ([]*ent.PluginStorage, int, error) {
			if len(ids) == 0 {
				return nil, 0, nil
			}
			rows, err := svc.db.PluginStorage.Query().Where(pluginstorage.IDGT(lastID), pluginstorage.InstallationIDIn(ids...)).Order(ent.Asc(pluginstorage.FieldID)).Limit(16).All(hostCtx)
			next := 0
			if len(rows) > 0 {
				next = rows[len(rows)-1].ID
			}
			return rows, next, err
		}, func(row *ent.PluginStorage) ([]byte, bool, error) {
			data, err := json.Marshal(row)
			return data, true, err
		})
		return struct{}{}, err
	})
	return err
}

// Restore adds missing installations only. Existing installations are preserved
// with their live configuration and KV state. Imported plugins have no grants
// and remain disabled until the owner reviews them in plugin management.
func (svc *BackupService) restorePlugins(ctx context.Context, db *ent.Client, data BackupData) error {
	_, err := authz.RunAsHost(ctx, db, "restore-plugins", func(hostCtx context.Context) (struct{}, error) {
		installed := map[int]int{}
		for _, source := range data.Plugins {
			if source == nil || source.ID <= 0 || source.PluginID == "" {
				return struct{}{}, fmt.Errorf("invalid plugin installation in backup")
			}
			if _, duplicate := installed[source.ID]; duplicate {
				return struct{}{}, fmt.Errorf("duplicate plugin installation %d", source.ID)
			}
			exists, err := db.Plugin.Query().Where(entplugin.PluginIDEQ(source.PluginID)).Exist(hostCtx)
			if err != nil {
				return struct{}{}, err
			}
			if exists {
				installed[source.ID] = 0
				continue
			}
			manifest := source.Manifest
			digest := source.PackageSha256
			if len(source.Package) > 0 {
				pkg, err := runtimeplugin.ParsePackage(source.Package)
				if err != nil {
					return struct{}{}, fmt.Errorf("restore plugin %q: %w", source.PluginID, err)
				}
				if pkg.Manifest.ID != source.PluginID {
					return struct{}{}, fmt.Errorf("plugin package ID differs from backup installation")
				}
				manifest, digest = pkg.Manifest, pkg.SHA256
			}
			target, err := db.Plugin.Create().SetPluginID(source.PluginID).SetName(source.Name).SetVersion(source.Version).
				SetDescription(source.Description).SetManifest(manifest).SetPackage(source.Package).SetPackageSha256(digest).
				SetEnabled(false).SetPriority(source.Priority).SetConfig(source.Config).
				SetGrantedPermissions([]string{}).SetGrantedNetwork([]string{}).Save(hostCtx)
			if err != nil {
				return struct{}{}, err
			}
			installed[source.ID] = target.ID
		}
		for _, source := range data.PluginStorage {
			if source == nil {
				return struct{}{}, fmt.Errorf("invalid plugin storage in backup")
			}
			target, found := installed[source.InstallationID]
			if !found {
				return struct{}{}, fmt.Errorf("plugin storage references missing installation %d", source.InstallationID)
			}
			if target == 0 {
				continue
			}
			if err := db.PluginStorage.Create().SetInstallationID(target).SetNamespace(source.Namespace).SetKey(source.Key).
				SetValue(source.Value).SetNillableExpiresAt(source.ExpiresAt).Exec(hostCtx); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}
