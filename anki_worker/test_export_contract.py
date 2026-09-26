"""Cross-language contract: fixtures originate from Go's real SQLite exporter."""

import json
import unittest
from copy import deepcopy
from pathlib import Path
from types import SimpleNamespace

from .config import WorkerError
from .snapshot import unique_object, validate_snapshot


class GoExportContractTests(unittest.TestCase):
    fixtures = Path(__file__).resolve().parents[1] / "internal/storage/testdata"
    config = SimpleNamespace(namespace="english-mcp", owner="owner:日本語")

    def load(self, name):
        return json.loads(
            (self.fixtures / f"export-{name}.json").read_text(encoding="utf-8"),
            object_pairs_hook=unique_object,
        )

    def test_empty_go_export_remains_an_authoritative_empty_snapshot(self):
        result = validate_snapshot(self.load("empty"), self.config)
        self.assertEqual(result.items, {})

    def test_go_export_unicode_nulls_and_nested_media_contract(self):
        payload = self.load("full")
        result = validate_snapshot(payload, self.config)
        self.assertEqual(len(result.items), 2)
        rich = next(item for item in result.items.values() if item["itemId"] == "item-rich")
        self.assertIn("<meaning> & café\u2028line\u2029paragraph 🦆", rich["customDescription"])
        self.assertIsNone(rich["sense"]["definition"]["examples"])
        self.assertEqual(rich["images"][0]["contentType"], "image/png")
        self.assertEqual(len(rich["lookup"]["entries"][0]["audio"]["uk"]["mediaId"]), 64)
        minimal = next(item for item in result.items.values() if item["itemId"] == "item-minimal")
        self.assertEqual(minimal["notes"], [])
        self.assertNotIn("lookup", minimal)
        self.assertNotIn("sense", minimal)
        self.assertEqual(minimal["context"], "context-only meaning")

    def test_changed_go_export_fails_digest_verification(self):
        payload = deepcopy(self.load("full"))
        payload["items"][0]["vocabulary"]["term"] = "tampered"
        with self.assertRaisesRegex(WorkerError, "digest"):
            validate_snapshot(payload, self.config)
