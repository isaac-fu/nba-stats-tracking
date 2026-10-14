from contextlib import contextmanager
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from uuid import UUID

from orchestration import pipeline


RELEASE = UUID("11111111-1111-4111-8111-111111111111")
SCHEMA = "build_" + RELEASE.hex


class ArtifactGateTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name)
        self.context = {
            "release_id": str(RELEASE), "schema": SCHEMA,
            "input_manifest_sha256": "a" * 64, "inputs": [],
            "code_sha": "b" * 64, "seed_manifest_sha256": "c" * 64,
            "source_name": "demo_fixture", "sample_data": True,
            "dbt_version": "1.12.5", "dbt_adapter_version": "1.11.0",
        }
        marker = "release_id=" + str(RELEASE) + " input_manifest_sha256=" + "a" * 64
        self.manifest = {
            "metadata": {"dbt_version": "1.12.5", "invocation_id": "one-build"},
            "nodes": {
                "model.nba_stats." + name: {
                    "resource_type": "model", "package_name": "nba_stats",
                    "schema": SCHEMA, "compiled_code": "-- " + marker + "\nselect 1",
                } for name in pipeline.MODELS
            },
        }
        self.manifest["nodes"]["test.nba_stats.integrity"] = {
            "resource_type": "test", "package_name": "nba_stats",
        }
        self.manifest["nodes"]["seed.nba_stats.team_identity"] = {
            "resource_type": "seed", "package_name": "nba_stats", "schema": SCHEMA,
        }
        self.results = {
            "metadata": {"invocation_id": "one-build"}, "args": {"which": "build"},
            "results": [
                {"unique_id": key, "status": "pass" if node["resource_type"] == "test" else "success"}
                for key, node in self.manifest["nodes"].items()
            ],
        }
        self.patches = [
            patch.object(pipeline, "code_hash", return_value="b" * 64),
            patch.object(pipeline, "seed_hash", return_value="c" * 64),
            patch.object(pipeline, "installed_versions", return_value=("1.12.5", "1.11.0")),
        ]
        for mock_patch in self.patches:
            mock_patch.start()
            self.addCleanup(mock_patch.stop)

    def check(self):
        for name, value in [
            ("build_context.json", self.context), ("manifest.json", self.manifest),
            ("run_results.json", self.results),
        ]:
            self.path.joinpath(name).write_text(json.dumps(value))
        return pipeline.validate_artifacts(self.path, self.context)

    def test_complete_successful_build_is_accepted(self):
        self.assertEqual(self.check()["nodes"], 10)

    def test_missing_model_result_is_rejected(self):
        self.results["results"].pop(0)
        with self.assertRaisesRegex(pipeline.PipelineError, "missing"):
            self.check()

    def test_missing_test_result_is_rejected(self):
        self.results["results"] = [r for r in self.results["results"] if not r["unique_id"].startswith("test.")]
        with self.assertRaisesRegex(pipeline.PipelineError, "missing"):
            self.check()

    def test_warning_or_failed_test_is_rejected(self):
        for result in self.results["results"]:
            if result["unique_id"].startswith("test."):
                result["status"] = "warn"
        with self.assertRaisesRegex(pipeline.PipelineError, "did not pass"):
            self.check()

    def test_artifacts_from_different_invocations_are_rejected(self):
        self.results["metadata"]["invocation_id"] = "other-build"
        with self.assertRaisesRegex(pipeline.PipelineError, "different"):
            self.check()

    def test_wrong_build_schema_is_rejected(self):
        self.manifest["nodes"]["model.nba_stats.dim_player"]["schema"] = "analytics"
        with self.assertRaisesRegex(pipeline.PipelineError, "schema"):
            self.check()

    def test_wrong_release_or_input_marker_is_rejected(self):
        self.manifest["nodes"]["model.nba_stats.dim_player"]["compiled_code"] = "select 1"
        with self.assertRaisesRegex(pipeline.PipelineError, "exact input freeze"):
            self.check()

    def test_selected_models_do_not_prove_full_build(self):
        self.results["args"]["select"] = ["dim_player"]
        with self.assertRaisesRegex(pipeline.PipelineError, "Selected"):
            self.check()

    def test_dbt_run_without_data_tests_is_rejected(self):
        self.results["args"]["which"] = "run"
        with self.assertRaisesRegex(pipeline.PipelineError, "full dbt build"):
            self.check()

    def test_changed_seed_cannot_be_published(self):
        with patch.object(pipeline, "seed_hash", return_value="d" * 64):
            with self.assertRaisesRegex(pipeline.PipelineError, "changed"):
                self.check()


class PublisherFailureTests(unittest.TestCase):
    def test_failed_copy_does_not_execute_pointer_swap(self):
        """Failure during copy must leave pointer writes outside the executed path.

        Real PostgreSQL rollback is exercised by the pipeline integration check;
        this test guards the publisher's order of operations without a database.
        """
        executed = []
        class Cursor:
            rowcount = 1
            def fetchone(self):
                return {"status": "candidate", "previous_release_id": None, "build_run_id": RELEASE}
        class FakeConnection:
            def execute(self, query, params=None):
                text = query.as_string() if hasattr(query, "as_string") else query
                executed.append(text)
                if "FROM api.published_release" in text:
                    return type("Empty", (), {"fetchone": lambda _: None})()
                return Cursor()
        @contextmanager
        def fake_connection():
            yield FakeConnection()
        context = {"schema": SCHEMA, "input_manifest_sha256": pipeline.hash_json([])}
        fingerprints = {model: {"count": 1, "sha256": "a" * 64} for model in pipeline.MODELS}
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(pipeline, "context_for", return_value=context), \
                 patch.object(pipeline, "validate_artifacts", return_value={}), \
                 patch.object(pipeline, "connection", fake_connection), \
                 patch.object(pipeline, "_input_rows", return_value=[]), \
                 patch.object(pipeline, "_read_seal", return_value={"tables": fingerprints}), \
                 patch.object(pipeline, "_lock_build_tables"), \
                 patch.object(pipeline, "_fingerprints", return_value=fingerprints), \
                 patch.object(pipeline, "_parity", side_effect=pipeline.PipelineError("bad rate")):
                with self.assertRaisesRegex(pipeline.PipelineError, "bad rate"):
                    pipeline.publish(RELEASE, Path(directory))
        self.assertTrue(any("INSERT INTO analytics." in query for query in executed))
        self.assertFalse(any("INSERT INTO api.published_release" in query for query in executed))
        self.assertFalse(any("INSERT INTO meta.publication_event" in query for query in executed))

    def test_changed_build_table_is_rejected_before_copy(self):
        executed = []
        class Cursor:
            def fetchone(self):
                return {"status": "candidate", "previous_release_id": None, "build_run_id": RELEASE}
        class FakeConnection:
            def execute(self, query, params=None):
                text = query.as_string() if hasattr(query, "as_string") else query
                executed.append(text)
                if "FROM api.published_release" in text:
                    return type("Empty", (), {"fetchone": lambda _: None})()
                return Cursor()
        @contextmanager
        def fake_connection():
            yield FakeConnection()
        context = {"schema": SCHEMA, "input_manifest_sha256": pipeline.hash_json([])}
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(pipeline, "context_for", return_value=context), \
                 patch.object(pipeline, "validate_artifacts", return_value={}), \
                 patch.object(pipeline, "connection", fake_connection), \
                 patch.object(pipeline, "_input_rows", return_value=[]), \
                 patch.object(pipeline, "_read_seal", return_value={"tables": {"x": "before"}}), \
                 patch.object(pipeline, "_lock_build_tables"), \
                 patch.object(pipeline, "_fingerprints", return_value={"x": "after"}):
                with self.assertRaisesRegex(pipeline.PipelineError, "changed after"):
                    pipeline.publish(RELEASE, Path(directory))
        self.assertFalse(any("INSERT INTO analytics." in query for query in executed))
        self.assertFalse(any("INSERT INTO api.published_release" in query for query in executed))


if __name__ == "__main__":
    unittest.main()
