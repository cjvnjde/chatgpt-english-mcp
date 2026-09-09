import hashlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from build_expressions import (
    cached_input,
    canonicalize_forms,
    magpie_entries,
    new_entry,
)


class CanonicalizeFormsTests(unittest.TestCase):
    def test_form_chain_retains_intermediate_usage_evidence(self):
        entries = {
            term: new_entry(term)
            for term in ("upon the matter", "upon the whole matter", "on the whole")
        }
        entries["upon the whole matter"].update(wiktionary=True, restricted=True)
        entries["on the whole"].update(wordnet=True, wordnet_tags=4)
        targets = {
            "upon the matter": {"upon the whole matter"},
            "upon the whole matter": {"on the whole"},
        }

        canonicalize_forms(entries, targets, {"on the whole"})

        self.assertNotIn("upon the matter", entries)
        self.assertIn("upon the matter", entries["upon the whole matter"]["aliases"])
        self.assertNotIn("upon the matter", entries["on the whole"]["aliases"])
        self.assertTrue(entries["upon the whole matter"]["restricted"])


class CachedInputTests(unittest.TestCase):
    def test_bad_download_does_not_poison_cache_or_prevent_retry(self):
        content = b"pinned source"
        metadata = {
            "url": "https://example.invalid/source",
            "sha256": hashlib.sha256(content).hexdigest(),
        }
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory)
            with (
                patch(
                    "build_expressions.urllib.request.urlopen",
                    return_value=io.BytesIO(b"corrupt download"),
                ),
                self.assertRaises(ValueError),
            ):
                cached_input(cache, "source", metadata, offline=False)
            self.assertFalse((cache / "source").exists())
            self.assertFalse((cache / "source.part").exists())
            with patch(
                "build_expressions.urllib.request.urlopen",
                return_value=io.BytesIO(content),
            ):
                path = cached_input(cache, "source", metadata, offline=False)
            self.assertEqual(path.read_bytes(), content)

    def test_interrupted_download_removes_partial_file(self):
        metadata = {"url": "https://example.invalid/source", "sha256": "unused"}
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory)
            with patch("build_expressions.urllib.request.urlopen") as request:
                request.return_value.__enter__.return_value.read.side_effect = [
                    b"partial source",
                    OSError("connection interrupted"),
                ]
                with self.assertRaises(OSError):
                    cached_input(cache, "source", metadata, offline=False)
            self.assertFalse((cache / "source").exists())
            self.assertFalse((cache / "source.part").exists())


class MagpieSourceTests(unittest.TestCase):
    def test_boolean_confidence_is_not_numeric_evidence(self):
        row = {
            "id": "instance",
            "idiom": "spill the beans",
            "confidence": True,
            "label": "i",
            "document_id": "document",
            "context": ["", "", "spill the beans", "", ""],
            "offsets": [[0, 15]],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "magpie.jsonl"
            path.write_text(json.dumps(row) + "\n", encoding="utf-8")
            with self.assertRaises(ValueError):
                magpie_entries(path, {})


if __name__ == "__main__":
    unittest.main()
