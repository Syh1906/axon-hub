import importlib.util
import datetime as dt
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("maintenance", Path(__file__).with_name("maintenance.py"))
maintenance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(maintenance)


class MaintenanceTests(unittest.TestCase):
    def test_restore_never_overwrites_online_database(self):
        with patch.object(maintenance, "run") as run:
            with self.assertRaises(ValueError):
                maintenance.restore_export({"postgres": {"database": "online"}}, "unused.json", "online")
            run.assert_not_called()

    def test_restore_rejects_sql_identifier_injection(self):
        with self.assertRaises(ValueError):
            maintenance.restore_export({"postgres": {"database": "online"}}, "unused.json", 'new; DROP DATABASE online')

    def test_receipt_checks_content_and_directory_boundary(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "axonhub-export-20261101T000000.123Z.dump"
            archive.write_bytes(b"PGDMPsynthetic-fixture")
            receipt = root / "fixture.json"
            value = {"file": archive.name, "bytes": archive.stat().st_size, "sha256": maintenance.digest(archive)}
            receipt.write_text(json.dumps(value))
            config = {"export_directory": str(root)}
            self.assertEqual(maintenance.verify_export(config, receipt)[0], archive)
            archive.write_bytes(b"PGDMPmodified-fixture")
            with self.assertRaises(ValueError):
                maintenance.verify_export(config, receipt)
            value["file"] = "../outside.dump"
            receipt.write_text(json.dumps(value))
            with self.assertRaises(ValueError):
                maintenance.verify_export(config, receipt)

    def test_capacity_reserves_headroom(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(maintenance.shutil, "disk_usage", return_value=maintenance.shutil._ntuple_diskusage(100*maintenance.GIB, 90*maintenance.GIB, 10*maintenance.GIB)):
                result = maintenance.capacity({"monitor_paths": [temporary]})
                self.assertEqual(result[0]["level"], "critical")
                self.assertEqual(result[0]["reserve_bytes"], 15*maintenance.GIB)
                with self.assertRaises(RuntimeError):
                    maintenance.require_capacity({}, temporary)

    def test_restore_uses_database_size_not_compressed_dump(self):
        with tempfile.TemporaryDirectory() as temporary:
            config = {"postgres": {"database": "online", "restore_monitor_path": temporary}}
            with patch.object(maintenance, "verify_export", return_value=(Path("unused"), {"bytes": 1, "source_database_bytes": 20*maintenance.GIB})), \
                 patch.object(maintenance, "require_capacity", side_effect=RuntimeError("space")) as capacity, \
                 patch.object(maintenance, "sql") as sql:
                with self.assertRaises(RuntimeError):
                    maintenance.restore_export(config, "unused", "isolated")
                self.assertEqual(capacity.call_args.args[2], 40*maintenance.GIB)
                sql.assert_not_called()

    def test_remote_execution_prefix_rejected_before_mutation(self):
        config = {"postgres": {"exec_prefix": ["docker", "exec", "postgres"]}}
        with patch.object(maintenance, "sql") as sql:
            with self.assertRaises(ValueError):
                maintenance.export_database(config)
            sql.assert_not_called()

    def test_export_expiration_is_previewed_and_keeps_latest_two(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for n in range(4):
                name = f"axonhub-export-2025010{n+1}T000000Z.dump"
                (root / name).write_bytes(b"PGDMPfixture")
                (root / Path(name).with_suffix(".json")).write_text(json.dumps({"file": name, "bytes": 12,
                    "created_at": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=20-n)).isoformat()}))
            unrelated = root / "manual.dump"
            unrelated.write_bytes(b"keep")
            config = {"export_directory": temporary}
            result = maintenance.expire_exports(config)
            self.assertEqual(len(result["files"]), 4)
            self.assertEqual(len(list(root.glob("*.dump"))), 5)
            maintenance.expire_exports(config, apply=True)
            self.assertEqual(len(list(root.glob("axonhub-export-*.dump"))), 2)
            self.assertEqual(unrelated.read_bytes(), b"keep")

    def test_shared_os_lock_rejects_duplicate_work(self):
        with tempfile.TemporaryDirectory() as temporary:
            config = {"state_directory": temporary}
            with maintenance.maintenance_lock(config):
                with self.assertRaises((OSError, RuntimeError)):
                    with maintenance.maintenance_lock(config):
                        self.fail("duplicate lock was granted")


if __name__ == "__main__":
    unittest.main()
