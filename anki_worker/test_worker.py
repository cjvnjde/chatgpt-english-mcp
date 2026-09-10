import hashlib
import json
import os
import signal
import tempfile
import time
import unittest
from contextlib import closing
from copy import deepcopy
from dataclasses import replace
from datetime import datetime, timedelta, timezone
from html import unescape
from pathlib import Path
from threading import Event, Thread
from unittest.mock import patch

from anki.collection import Collection
from anki.errors import SyncError, SyncErrorKind
from anki.sync import SyncOutput

from .__main__ import inspect_status, main
from .adapter import AnkiAdapter
from .config import Config, StopRequested, TransientError, WorkerError
from .reconcile import FIELDS, MODEL_NAME, reconcile
from .rendering import BACK, CSS, FRONT, render
from .snapshot import source_id, unique_object, validate_snapshot
from .storage import Store, atomic_json, load_json
from .worker import Worker


def vocabulary(item_id="one", **changes):
    item = {
        "itemId": item_id,
        "term": "word",
        "normalizedTerm": "word",
        "status": "new",
        "usefulness": "normal",
        "personalInterest": "normal",
        "tags": [],
        "notes": [],
        "examples": [],
        "createdAt": "2026-09-01T00:00:00Z",
        "updatedAt": "2026-09-01T00:00:00Z",
    }
    item.update(changes)
    return item


def envelope(config, items):
    payload = {
        "schemaVersion": 3,
        "namespace": config.namespace,
        "owner": config.owner,
        "digest": "",
        "itemCount": len(items),
        "complete": True,
        "items": [
            {
                "sourceId": source_id(config.namespace, config.owner, item["itemId"]),
                "vocabulary": item,
            }
            for item in items
        ],
    }
    encoded = json.dumps(payload["items"], ensure_ascii=False, separators=(",", ":"))
    encoded = encoded.translate(
        {ord(char): f"\\u{ord(char):04x}" for char in "&<>\u2028\u2029"}
    )
    payload["digest"] = hashlib.sha256(encoded.encode()).hexdigest()
    return payload


def add_basic(collection, deck_id, *, shared=False):
    model = collection.models.by_name(
        "Basic (and reversed card)" if shared else "Basic"
    )
    note = collection.new_note(model)
    note["Front"] = "Unrelated question"
    note["Back"] = "Unrelated answer"
    collection.add_note(note, deck_id)
    return note


class RealCollectionCase(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.config = Config(
            True,
            "http://localhost/internal/anki/snapshot",
            "t" * 32,
            "disposable@example.invalid",
            "secret",
            collection_path=Path(self.temporary.name) / "collection.anki2",
        )
        self.store = Store(self.config)
        self.store.load()
        self.collection = Collection(str(self.config.collection_path))
        self.addCleanup(self.collection.close)

    def snapshot(self, *items):
        return validate_snapshot(envelope(self.config, list(items)), self.config)

    def apply(self, *items):
        return reconcile(self.collection, self.store, self.snapshot(*items))

    def managed_note(self, item_id="one"):
        return self.collection.get_note(
            self.store.state["notes"][
                source_id(self.config.namespace, self.config.owner, item_id)
            ]
        )


class ReconciliationTests(RealCollectionCase):
    def test_source_link_preserves_decomposed_unicode_destination(self):
        url = "https://example.com/cafe\u0301?term=e\u0301&tag=é"
        item = vocabulary(descriptionSource={"title": "Source", "url": url})
        self.apply(item)
        self.assertIn(f'href="{url}"', unescape(self.managed_note()["Source"]))
        self.assertFalse(any(self.apply(item).values()))

    def test_usefulness_changes_leave_anki_content_and_schedule_unchanged(self):
        item = vocabulary(usefulness="low")
        self.apply(item)
        note = self.managed_note()
        card = note.cards()[0]
        card.queue = 2
        card.type = 2
        card.ivl = 14
        card.due = 40
        card.reps = 5
        self.collection.update_card(card)

        item["usefulness"] = "high"
        self.apply(item)
        restored = self.managed_note()
        restored_card = restored.cards()[0]
        self.assertEqual(
            (restored.id, restored.fields, restored.tags),
            (note.id, note.fields, note.tags),
        )
        self.assertEqual(
            (
                restored_card.id,
                restored_card.queue,
                restored_card.ivl,
                restored_card.due,
                restored_card.reps,
            ),
            (card.id, 2, 14, 40, 5),
        )

    def test_remote_content_edits_and_movement_preserve_card_and_schedule(self):
        item = vocabulary(
            tags=["Tag", "tag", "two words"], customDescription="Original"
        )
        self.apply(item)
        note = self.managed_note()
        card = note.cards()[0]
        card.queue = 2
        card.type = 2
        card.ivl = 19
        card.due = 45
        card.reps = 7
        self.collection.update_card(card)
        other = self.collection.decks.id("Unrelated")
        self.collection.set_deck([card.id], other)
        note["SourceID"] = "edited identity"
        note["Meaning"] = "remote edit"
        note.tags = ["manual"]
        self.collection.update_note(note)
        self.apply(item)
        restored = self.managed_note()
        result = restored.cards()[0]
        self.assertEqual(restored.id, note.id)
        self.assertEqual(
            (result.id, result.ivl, result.due, result.reps, result.queue),
            (card.id, 19, 45, 7, 2),
        )
        self.assertEqual(result.did, self.store.state["deckId"])
        self.assertEqual(
            (restored.fields, sorted(restored.tags)),
            render(source_id(self.config.namespace, self.config.owner, "one"), item),
        )
        self.assertFalse(any(self.apply(item).values()))

    def test_same_word_senses_stay_distinct_and_deleted_note_is_recreated(self):
        first, second = vocabulary("sense1"), vocabulary("sense2")
        self.apply(first, second)
        left, right = self.managed_note("sense1"), self.managed_note("sense2")
        self.assertNotEqual(left.id, right.id)
        self.collection.remove_notes([left.id])
        self.apply(first, second)
        self.assertNotEqual(self.managed_note("sense1").id, left.id)
        self.assertEqual(self.managed_note("sense2").id, right.id)

    def test_empty_snapshot_deletes_moved_owned_and_manual_notes_only(self):
        self.apply(vocabulary())
        note = self.managed_note()
        other = self.collection.decks.id("Unrelated")
        self.collection.set_deck([note.cards()[0].id], other)
        unrelated = add_basic(self.collection, other)
        manual = add_basic(self.collection, self.store.state["deckId"])
        self.apply()
        self.assertEqual(self.collection.find_notes(""), [unrelated.id])
        self.assertNotIn(manual.id, self.collection.find_notes(""))

    def test_shared_manual_note_keeps_unrelated_card_and_review_state(self):
        self.apply()
        managed = self.store.state["deckId"]
        note = add_basic(self.collection, managed, shared=True)
        _, unrelated_card = note.cards()
        other = self.collection.decks.id("Unrelated")
        self.collection.set_deck([unrelated_card.id], other)
        unrelated_card = self.collection.get_card(unrelated_card.id)
        unrelated_card.reps = 11
        unrelated_card.ivl = 32
        self.collection.update_card(unrelated_card)
        self.apply()
        self.assertEqual(self.collection.card_ids_of_note(note.id), [unrelated_card.id])
        retained = self.collection.get_card(unrelated_card.id)
        self.assertEqual((retained.did, retained.reps, retained.ivl), (other, 11, 32))
        self.assertEqual(self.collection.get_note(note.id)["Back"], "Unrelated answer")

    def test_duplicate_recovery_and_crash_gap_keep_canonical_card(self):
        item = vocabulary()
        self.apply(item)
        canonical = self.managed_note()
        model = self.collection.models.get(self.store.state["modelId"])
        duplicate = self.collection.new_note(model)
        duplicate.fields = list(canonical.fields)
        self.collection.add_note(duplicate, self.store.state["deckId"])
        self.apply(item)
        self.assertEqual(self.collection.find_notes(""), [canonical.id])
        del self.store.state["notes"][
            source_id(self.config.namespace, self.config.owner, "one")
        ]
        self.store.save()
        self.apply(item)
        self.assertEqual(self.managed_note().id, canonical.id)
        self.assertEqual(len(self.managed_note().cards()), 1)

    def test_missing_canonical_stays_owned_at_each_mapping_commit(self):
        for use_duplicate in (False, True):
            with self.subTest(use_duplicate=use_duplicate):
                self.apply(vocabulary())
                old = self.managed_note()
                replacement = None
                if use_duplicate:
                    replacement = self.collection.new_note(old.note_type())
                    replacement.fields = list(old.fields)
                    self.collection.add_note(replacement, self.store.state["deckId"])
                self.collection.remove_notes([old.id])
                save = self.store.save

                def interrupted_save(save=save, old=old, replacement=replacement):
                    save()
                    persisted = load_json(self.store.state_path)
                    current = next(iter(persisted["notes"].values()))
                    if current != old.id:
                        self.assertIn(old.id, persisted["pendingDeletes"])
                        if replacement is not None:
                            self.assertEqual(current, replacement.id)
                        raise OSError("interrupted immediately after mapping commit")

                with (
                    patch.object(self.store, "save", side_effect=interrupted_save),
                    self.assertRaisesRegex(OSError, "mapping commit"),
                ):
                    self.apply(vocabulary())
                self.store.load()
                self.apply(vocabulary())
                self.assertIn(old.id, self.store.state["pendingDeletes"])

    def test_crash_gap_recovered_deletion_is_durable_before_note_removal(self):
        self.apply(vocabulary())
        note = self.managed_note()
        self.store.state["notes"] = {}
        self.store.save()
        with (
            patch.object(
                self.collection, "remove_notes", side_effect=OSError("interrupted")
            ),
            self.assertRaises(OSError),
        ):
            self.apply()
        note["SourceID"] = "remote identity edit"
        self.collection.update_note(note)
        other = self.collection.decks.id("Other")
        self.collection.set_deck([note.cards()[0].id], other)
        self.store.load()
        self.apply()
        self.assertEqual(self.collection.find_notes(""), [])

    def test_pending_deletion_refuses_remotely_converted_note(self):
        self.apply(vocabulary())
        note = self.managed_note()
        self.store.state["notes"] = {}
        self.store.state["pendingDeletes"] = [note.id]
        self.store.save()
        info = self.collection.models.change_notetype_info(
            old_notetype_id=note.mid,
            new_notetype_id=self.collection.models.by_name("Basic (and reversed card)")[
                "id"
            ],
        )
        info.input.note_ids.append(note.id)
        self.collection.models.change_notetype_of_notes(info.input)
        converted = self.collection.get_note(note.id)
        before = (converted.fields, self.collection.card_ids_of_note(note.id))
        with self.assertRaises(WorkerError):
            self.apply()
        self.assertEqual(
            (
                self.collection.get_note(note.id).fields,
                self.collection.card_ids_of_note(note.id),
            ),
            before,
        )

    def assert_creation_crash_recovers(self, identity_key):
        save = self.store.save

        def interrupted_save():
            if self.store.state[identity_key] is not None:
                raise OSError("simulated interruption before identity persistence")
            save()

        with (
            patch.object(self.store, "save", side_effect=interrupted_save),
            self.assertRaises(OSError),
        ):
            self.apply(vocabulary())
        created_id = self.store.state[identity_key]
        self.store.load()
        self.apply(vocabulary())
        self.assertEqual(self.store.state[identity_key], created_id)
        self.assertEqual(self.collection.find_notes(""), [self.managed_note().id])
        self.assertEqual(
            self.collection.decks.by_name(self.config.deck)["id"],
            self.store.state["deckId"],
        )
        self.assertEqual(
            self.collection.models.by_name(MODEL_NAME)["id"],
            self.store.state["modelId"],
        )

    def test_deck_creation_crash_recovers_without_taking_unrelated_decks(self):
        self.assert_creation_crash_recovers("deckId")

    def test_model_creation_crash_recovers_without_duplicate_types(self):
        self.assert_creation_crash_recovers("modelId")

    def test_source_id_collision_cannot_steal_another_tracked_identity(self):
        self.apply(vocabulary("one"), vocabulary("two"))
        first, second = self.managed_note("one"), self.managed_note("two")
        first["SourceID"] = second["SourceID"]
        self.collection.update_note(first)
        self.apply(vocabulary("one"), vocabulary("two"))
        self.assertEqual(self.managed_note("one").id, first.id)
        self.assertEqual(self.managed_note("two").id, second.id)

    def test_template_css_field_and_display_name_repair_keep_ids(self):
        self.apply(vocabulary())
        note = self.managed_note()
        card_id = note.cards()[0].id
        deck_id, model_id = self.store.state["deckId"], self.store.state["modelId"]
        self.collection.decks.rename(deck_id, "Renamed")
        model = self.collection.models.get(model_id)
        model["name"] = "Renamed type"
        model["flds"][0]["name"] = "Changed source"
        model["tmpls"][0]["qfmt"] = "{{Word}} edited"
        model["tmpls"][0]["afmt"] = "{{Meaning}} edited"
        model["css"] = "remote css"
        self.collection.models.update_dict(model)
        self.apply(vocabulary())
        restored = self.collection.models.get(model_id)
        self.assertEqual([field["name"] for field in restored["flds"]], list(FIELDS))
        self.assertEqual(
            (
                restored["css"],
                restored["tmpls"][0]["qfmt"],
                restored["tmpls"][0]["afmt"],
            ),
            (CSS, FRONT, BACK),
        )
        self.assertEqual(self.managed_note().cards()[0].id, card_id)
        self.assertEqual(self.store.state["deckId"], deck_id)

    def test_incompatible_schema_refuses_with_recoverable_backup(self):
        self.apply(vocabulary())
        model = self.collection.models.get(self.store.state["modelId"])
        self.collection.models.add_field(
            model, self.collection.models.new_field("Personal field")
        )
        self.collection.models.update_dict(model)
        note = self.managed_note()
        note["Personal field"] = "must survive"
        self.collection.update_note(note)
        with self.assertRaisesRegex(WorkerError, "backup:"):
            self.apply()
        self.assertEqual(
            self.collection.get_note(note.id)["Personal field"], "must survive"
        )
        backups = list((Path(self.temporary.name) / "backups").iterdir())
        self.assertEqual(len(backups), 1)
        self.assertTrue((backups[0] / "collection.anki2").exists())
        self.assertTrue((backups[0] / "worker-state.json").exists())

    def test_unmanaged_existing_deck_is_not_taken_over(self):
        deck = self.collection.decks.id(self.config.deck)
        unrelated = add_basic(self.collection, deck)
        with self.assertRaisesRegex(WorkerError, "untracked deck"):
            self.apply()
        self.assertEqual(
            self.collection.get_note(unrelated.id)["Back"], "Unrelated answer"
        )

    def test_unmanaged_existing_model_is_not_taken_over(self):
        model = self.collection.models.by_name("Basic")
        model["name"] = MODEL_NAME
        self.collection.models.update_dict(model)
        with self.assertRaisesRegex(WorkerError, "matching persisted ownership"):
            self.apply()
        self.assertIsNone(self.store.state["deckId"])


class ValidationTests(unittest.TestCase):
    def setUp(self):
        self.config = Config(True, "http://localhost", "t" * 32, "account", "password")

    def test_incomplete_malformed_duplicate_and_wrong_identity_fail_closed(self):
        good = envelope(self.config, [vocabulary()])
        variants = []
        for key, value in (
            ("complete", False),
            ("itemCount", 0),
            ("items", None),
            ("owner", "other"),
            ("digest", "not-a-digest"),
            ("schemaVersion", True),
            ("schemaVersion", 1),
            ("schemaVersion", 2),
        ):
            payload = deepcopy(good)
            payload[key] = value
            variants.append(payload)
        missing = deepcopy(good)
        del missing["items"][0]["vocabulary"]["term"]
        variants.append(missing)
        duplicate = deepcopy(good)
        duplicate["items"] *= 2
        duplicate["itemCount"] = 2
        variants.append(duplicate)
        nested = deepcopy(good)
        nested["items"][0]["vocabulary"]["descriptionSource"] = {"url": ["bad"]}
        variants.append(nested)
        unknown_usefulness = deepcopy(good)
        unknown_usefulness["items"][0]["vocabulary"]["usefulness"] = "urgent"
        variants.append(unknown_usefulness)
        unknown_interest = deepcopy(good)
        unknown_interest["items"][0]["vocabulary"]["personalInterest"] = "urgent"
        variants.append(unknown_interest)
        for payload in variants:
            with self.subTest(payload=payload), self.assertRaises(WorkerError):
                validate_snapshot(payload, self.config)
        with self.assertRaisesRegex(WorkerError, "duplicate object"):
            json.loads('{"items":[],"items":[]}', object_pairs_hook=unique_object)

    def test_context_only_sense_is_exported_to_anki(self):
        item = vocabulary(context="The river <bank>")
        snapshot = validate_snapshot(envelope(self.config, [item]), self.config)
        fields, _ = render(
            source_id(self.config.namespace, self.config.owner, "one"),
            next(iter(snapshot.items.values())),
        )
        self.assertEqual(fields[4], "The river &lt;bank&gt;")

    def test_rendering_escapes_links_deduplicates_examples_and_encodes_tags(self):
        item = vocabulary(
            term='<script>alert("word")</script>',
            tags=["A", "a", "a b", "a_b", "é", "", "A"],
            examples=["same", "same", "<img src=x>"],
            notes=["<b>note</b>"],
            descriptionSource={"title": "<source>", "url": "javascript:alert(1)"},
        )
        fields, tags = render("source", item)
        self.assertNotIn("<script>", "".join(fields))
        self.assertNotIn("javascript:", "".join(fields))
        self.assertIn("&lt;script&gt;", fields[1])
        self.assertEqual(fields[3].count("<p>same</p>"), 1)
        self.assertIn("No definition saved", fields[2])
        self.assertEqual(len({tag.casefold() for tag in tags}), 6)
        self.assertTrue(all(not any(char.isspace() for char in tag) for tag in tags))

    def test_lookup_definitions_and_selected_sense_rendering(self):
        definition = {
            "definition": "meaning",
            "examples": ["same", "sense example"],
            "phrases": [],
            "seeAlso": [],
            "images": [],
            "labels": [],
        }
        item = vocabulary(
            examples=["same"],
            sense={
                "entryIndex": 0,
                "definitionIndex": 0,
                "headword": "word",
                "partOfSpeech": "noun",
                "context": "saved context",
                "pronunciations": {"uk": "/wɜːd/", "us": "/wɝːd/"},
                "definition": definition,
            },
        )
        validate_snapshot(envelope(self.config, [item]), self.config)
        fields, _ = render("source", item)
        self.assertEqual(fields[2], "meaning")
        self.assertEqual(fields[3].count("<p>same</p>"), 1)
        self.assertIn("UK /wɜːd/", fields[6])
        item["customDescription"] = "custom"
        self.assertEqual(render("source", item)[0][2], "custom")
        del item["sense"]
        del item["customDescription"]
        item["lookup"] = {
            "entries": [
                {
                    "headword": "word",
                    "partOfSpeech": "noun",
                    "definitions": [definition],
                }
            ]
        }
        self.assertIn("word — noun", render("source", item)[0][2])


class AdapterTests(RealCollectionCase):
    def open_adapter(self):
        self.collection.close()
        adapter = AnkiAdapter(self.config, self.store)
        self.addCleanup(adapter.close)
        atomic_json(
            self.store.auth_path,
            {
                "identity": self.config.identity,
                "hkey": "private-auth-key",
                "endpoint": "",
            },
        )
        adapter.authenticate()
        return adapter

    def test_official_redirect_is_persisted_without_exposing_auth(self):
        adapter = self.open_adapter()
        adapter.collection.sync_collection = lambda *args, **kwargs: SyncOutput(
            required=SyncOutput.NO_CHANGES, new_endpoint="https://sync2.ankiweb.net/"
        )
        self.assertEqual(adapter.sync(), "accepted")
        persisted = load_json(self.store.auth_path)
        self.assertEqual(persisted["endpoint"], "https://sync2.ankiweb.net/")
        self.assertEqual(self.store.auth_path.stat().st_mode & 0o777, 0o600)
        adapter.collection.sync_collection = lambda *args, **kwargs: SyncOutput(
            required=SyncOutput.NO_CHANGES,
            new_endpoint="http://attacker.invalid/private-auth-key",
        )
        with self.assertRaisesRegex(WorkerError, "unexpected sync host") as caught:
            adapter.sync()
        self.assertNotIn("private-auth-key", str(caught.exception))
        self.assertEqual(
            load_json(self.store.auth_path)["endpoint"], "https://sync2.ankiweb.net/"
        )

    def test_auth_rejection_clears_key_and_redacts_library_message(self):
        adapter = self.open_adapter()

        def denied(*args, **kwargs):
            raise SyncError(
                "server echoed private-auth-key and secret",
                None,
                None,
                None,
                SyncErrorKind.AUTH,
            )

        adapter.collection.sync_collection = denied
        with self.assertRaisesRegex(WorkerError, "authentication rejected") as caught:
            adapter.sync()
        self.assertFalse(self.store.auth_path.exists())
        self.assertNotIn("private-auth-key", str(caught.exception))
        self.assertNotIn("secret", str(caught.exception))

    def test_stop_aborts_active_adapter_operation_without_retry(self):
        adapter = self.open_adapter()
        self.store.stop_event = Event()
        aborted = Event()

        def active_operation():
            self.store.stop_event.set()
            self.assertTrue(aborted.wait(2), "active operation was not cancelled")

        with (
            patch.object(adapter.collection, "abort_sync", side_effect=aborted.set),
            self.assertRaises(StopRequested),
        ):
            adapter.call(active_operation)
        adapter.close()
        self.assertIsNone(adapter.collection.db)


class ControlledAdapter:
    def __init__(self, config, store, script):
        self.collection = Collection(str(config.collection_path))
        self.script = script
        self.store = store

    def authenticate(self, *, force=False):
        pass

    def close(self):
        self.collection.close()

    def sync(self):
        action = self.script["sync"].pop(0) if self.script["sync"] else "accepted"
        if isinstance(action, Exception):
            raise action
        if callable(action):
            return action(self)
        return action

    def full(self, *, upload):
        self.script.setdefault("full", []).append(upload)
        if upload and self.script.get("uploadError"):
            raise self.script["uploadError"]
        if not upload and self.script.get("download"):
            self.script["download"](self)

    def remote_changed(self):
        return (
            self.script.get("remote", []).pop(0) if self.script.get("remote") else False
        )


class WorkerCycleTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.config = Config(
            True,
            "http://localhost",
            "t" * 32,
            "account",
            "password",
            collection_path=Path(self.temporary.name) / "collection.anki2",
        )
        self.script = {"sync": []}
        self.items = [vocabulary()]
        self.sleeps = []
        self.worker = Worker(
            self.config,
            adapter_factory=lambda config, store: ControlledAdapter(
                config, store, self.script
            ),
            fetch=lambda config: validate_snapshot(
                envelope(config, self.items), config
            ),
            sleep=self.sleeps.append,
            jitter=lambda: 0,
        )

    def test_normalized_deck_name_converges_and_preserves_existing_parent(self):
        self.config = replace(self.config, deck=" CAFE\u0301 :: Vocabulary ")
        self.worker.config = self.config
        self.worker.store = Store(self.config)
        self.worker.store.load()
        with closing(Collection(str(self.config.collection_path))) as collection:
            parent = collection.decks.id("Café")
            unrelated = add_basic(collection, parent)

        self.assertTrue(self.worker.once()["healthy"])
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(self.worker.store.state["notes"].values()))
            card = collection.get_note(nid).cards()[0]
            self.assertEqual(collection.decks.get(card.did)["name"], "Café::Vocabulary")
            self.assertEqual(
                collection.get_note(unrelated.id)["Back"], "Unrelated answer"
            )
            self.assertEqual(collection.decks.get(parent)["name"], "Café")

    def test_decomposed_unicode_converges_without_rewriting_reviewed_cards(self):
        self.items = [
            vocabulary(
                customDescription="cafe\u0301",
                notes=["re\u0301sume\u0301"],
                descriptionSource={
                    "title": "Cafe\u0301",
                    "url": "https://example.com/cafe\u0301",
                },
                tags=["e\u0301", "é"],
            )
        ]
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(self.worker.store.state["notes"].values()))
            note = collection.get_note(nid)
            self.assertEqual(note["Meaning"], "café")
            self.assertEqual(note["Notes"], "<p>résumé</p>")
            self.assertEqual(sorted(note.tags), ["vocab::u65cc81", "vocab::uc3a9"])
            card = note.cards()[0]
            card.reps = 7
            card.ivl = 19
            card.due = 45
            card.queue = 2
            card.type = 2
            collection.update_card(card)

        result = self.worker.once()
        self.assertTrue(result["healthy"])
        self.assertEqual(result["counts"]["updated"], 0)
        with closing(Collection(str(self.config.collection_path))) as collection:
            restored = collection.get_note(nid).cards()[0]
            self.assertEqual(
                (
                    restored.id,
                    restored.reps,
                    restored.ivl,
                    restored.due,
                    restored.queue,
                ),
                (card.id, 7, 19, 45, 2),
            )

    def test_supported_snapshot_controls_converge_after_anki_field_normalization(self):
        self.items = [vocabulary(notes=["left\x1fright\x7f\nnext\tpart"])]
        self.assertTrue(self.worker.once()["healthy"])
        result = self.worker.once()
        self.assertTrue(result["healthy"])
        self.assertEqual(result["counts"]["updated"], 0)
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(self.worker.store.state["notes"].values()))
            self.assertEqual(
                collection.get_note(nid)["Notes"], "<p>leftright<br>next\tpart</p>"
            )

    def test_nul_text_still_fails_before_collection_mutation(self):
        self.worker.once()
        before = self.config.collection_path.read_bytes()
        self.items = [vocabulary(notes=["left\x00right"])]
        with self.assertRaisesRegex(WorkerError, "NUL"):
            self.worker.once()
        self.assertEqual(self.config.collection_path.read_bytes(), before)
        self.assertFalse(load_json(self.worker.store.status_path)["healthy"])

    def test_backup_preserves_collections_with_uri_delimiters_in_the_path(self):
        config = replace(
            self.config,
            collection_path=self.config.collection_path.with_name(
                "collection#backup?.anki2"
            ),
        )
        store = Store(config)
        store.load()
        with closing(Collection(str(config.collection_path))) as collection:
            note = add_basic(collection, collection.decks.id("Unrelated"))
            backup = store.backup()
        with closing(Collection(str(backup / config.collection_path.name))) as restored:
            self.assertEqual(restored.get_note(note.id)["Back"], "Unrelated answer")
        self.assertEqual(load_json(backup / store.state_path.name), store.state)

    def test_repeated_refusal_reuses_baseline_until_converged(self):
        self.worker.once()
        baseline = None
        for _ in range(8):
            self.script["sync"] = ["upload"]
            with self.assertRaisesRegex(WorkerError, "Unsafe full upload refused"):
                self.worker.once()
            status = load_json(self.worker.store.status_path)
            self.assertTrue(status["recoveryActive"])
            if baseline is None:
                baseline = status["recoveryBackup"]
            self.assertEqual(status["recoveryBackup"], baseline)
        root = Path(baseline).parent
        self.assertEqual(list(root.iterdir()), [Path(baseline)])
        self.script["sync"] = [WorkerError("still cannot recover")]
        with self.assertRaises(WorkerError):
            self.worker.once()
        self.assertTrue(self.worker.store.recovery_path.exists())
        self.assertTrue(self.worker.once()["healthy"])
        status = load_json(self.worker.store.status_path)
        self.assertFalse(status["recoveryActive"])
        self.assertEqual(status["recoveryBackup"], baseline)
        self.assertFalse(self.worker.store.recovery_path.exists())

    def test_retention_protects_oldest_unresolved_baseline(self):
        self.worker.once()
        store = self.worker.store
        baseline = store.recovery_backup()
        for _ in range(8):
            newest = store.backup()
        completed = set(baseline.parent.iterdir())
        self.assertEqual(len(completed), Store.BACKUP_LIMIT)
        self.assertIn(baseline, completed)
        self.assertIn(newest, completed)
        self.assertEqual(store.recovery_backup(), baseline)
        for backup in completed:
            self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
            for path in backup.iterdir():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        store.finish_recovery()
        for _ in range(Store.BACKUP_LIMIT):
            store.backup()
        self.assertFalse(baseline.exists())

    def test_interrupted_backup_never_becomes_completed_baseline(self):
        self.worker.once()
        store = self.worker.store

        def interrupted_completion(path, payload):
            if path.name == "complete.json":
                raise OSError("interrupted backup completion")
            atomic_json(path, payload)

        with (
            patch(
                "anki_worker.storage.atomic_json", side_effect=interrupted_completion
            ),
            self.assertRaisesRegex(OSError, "interrupted backup"),
        ):
            store.recovery_backup()
        incident = load_json(store.recovery_path)
        root = store.directory / "backups"
        self.assertEqual(list(root.iterdir()), [])
        partial = root / (".partial-" + incident["backup"])
        partial.mkdir()
        (partial / "interrupted-copy").write_bytes(b"partial")
        baseline = store.recovery_backup()
        self.assertEqual(baseline.name, incident["backup"])
        self.assertFalse(partial.exists())
        with (
            patch(
                "anki_worker.storage.atomic_json", side_effect=interrupted_completion
            ),
            self.assertRaises(OSError),
        ):
            store.backup()
        self.assertEqual(list(root.iterdir()), [baseline])
        self.assertEqual(store.recovery_backup(), baseline)
        with closing(
            Collection(str(baseline / self.config.collection_path.name))
        ) as restored:
            self.assertEqual(restored.note_count(), 1)

    def test_failed_export_never_opens_or_mutates_collection(self):
        self.worker.once()
        before = self.config.collection_path.read_bytes()

        def fail(config):
            raise WorkerError("incomplete source")

        self.worker.fetch = fail
        with self.assertRaisesRegex(WorkerError, "incomplete"):
            self.worker.once()
        self.assertEqual(self.config.collection_path.read_bytes(), before)
        self.assertFalse(load_json(self.worker.store.status_path)["healthy"])

    def test_snapshot_digest_mismatch_never_deletes_existing_notes(self):
        self.items = [vocabulary(notes=["<&>\u2028\u2029 café"])]
        self.worker.once()
        before = self.config.collection_path.read_bytes()
        payload = envelope(self.config, self.items)
        payload["items"] = []
        payload["itemCount"] = 0
        self.worker.fetch = lambda config: validate_snapshot(payload, config)
        with self.assertRaisesRegex(WorkerError, "digest"):
            self.worker.once()
        self.assertEqual(self.config.collection_path.read_bytes(), before)

    def test_initial_existing_account_download_preserves_unrelated_content(self):
        self.script["sync"] = ["download", "accepted"]

        def download(adapter):
            add_basic(
                adapter.collection, adapter.collection.decks.id("Existing account")
            )

        self.script["download"] = download
        self.worker.once()
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.note_count(), 2)
            self.assertEqual(len(collection.find_notes('deck:"Existing account"')), 1)
        self.assertEqual(self.script["full"], [False])

    def test_empty_account_bootstrap_and_later_unsafe_upload_refusal(self):
        self.script["sync"] = ["upload", "upload"]
        self.worker.once()
        self.assertEqual(self.script["full"], [True])
        self.script["sync"] = ["upload"]
        with self.assertRaisesRegex(WorkerError, "Unsafe full upload refused"):
            self.worker.once()
        self.assertEqual(self.script["full"], [True])

    def test_ambiguous_bootstrap_upload_is_not_blindly_retried(self):
        self.script["sync"] = ["upload", "upload"]
        self.script["uploadError"] = TransientError("ambiguous upload result")
        with self.assertRaisesRegex(TransientError, "ambiguous"):
            self.worker.once()
        self.assertEqual(self.script["full"], [True])
        self.assertNotIn("lastSuccess", load_json(self.worker.store.status_path))
        del self.script["uploadError"]
        self.script["sync"] = ["download", "accepted"]
        self.worker.once()
        self.assertEqual(self.script["full"], [True, False])

    def test_full_download_after_reconcile_repeats_projection(self):
        self.script["sync"] = ["accepted", "download", "accepted", "accepted"]

        def download(adapter):
            adapter.collection.remove_notes(adapter.collection.find_notes(""))
            add_basic(
                adapter.collection, adapter.collection.decks.id("Remote unrelated")
            )

        self.script["download"] = download
        result = self.worker.once()
        self.assertTrue(result["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.note_count(), 2)
            self.assertEqual(len(collection.find_notes('deck:"English MCP"')), 1)
        self.assertEqual(self.script["full"], [False])

    def test_deleted_identity_survives_restart_and_full_download(self):
        self.worker.once()
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(self.worker.store.state["notes"].values()))
            note = collection.get_note(nid)
            note["SourceID"] = "remote identity edit"
            collection.update_note(note)
            other = collection.decks.id("Remote unrelated")
            collection.set_deck([note.cards()[0].id], other)
            unrelated = add_basic(collection, other)
            remote_backup = (
                self.worker.store.backup() / self.config.collection_path.name
            )

        self.items = []
        self.script["sync"] = [
            "accepted",
            TransientError("offline"),
            TransientError("offline"),
            TransientError("offline"),
        ]
        with self.assertRaises(TransientError):
            self.worker.once()

        def download(adapter):
            adapter.collection.close()
            adapter.store.config.collection_path.write_bytes(remote_backup.read_bytes())
            adapter.collection = Collection(str(adapter.store.config.collection_path))

        self.script["sync"] = ["download", "accepted"]
        self.script["download"] = download
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.find_notes(""), [unrelated.id])
            self.assertEqual(
                collection.get_note(unrelated.id)["Back"], "Unrelated answer"
            )

    def test_recreated_note_retains_missing_identity_through_failed_sync_and_restore(
        self,
    ):
        self.worker.once()
        with closing(Collection(str(self.config.collection_path))) as collection:
            old_id = next(iter(self.worker.store.state["notes"].values()))
            note = collection.get_note(old_id)
            note["SourceID"] = "edited remote identity"
            collection.update_note(note)
            other = collection.decks.id("Remote unrelated")
            collection.set_deck([note.cards()[0].id], other)
            unrelated = add_basic(collection, other)
            remote_backup = (
                self.worker.store.backup() / self.config.collection_path.name
            )
            collection.remove_notes([old_id])
        self.script["sync"] = [
            "accepted",
            TransientError("offline"),
            TransientError("offline"),
            TransientError("offline"),
        ]
        with self.assertRaises(TransientError):
            self.worker.once()
        persisted = load_json(self.worker.store.state_path)
        self.assertNotEqual(next(iter(persisted["notes"].values())), old_id)
        self.assertIn(old_id, persisted["pendingDeletes"])

        def restore(adapter):
            self.assertIn(old_id, load_json(adapter.store.state_path)["pendingDeletes"])
            adapter.collection.close()
            adapter.store.config.collection_path.write_bytes(remote_backup.read_bytes())
            adapter.collection = Collection(str(adapter.store.config.collection_path))

        self.script["sync"] = ["download", "accepted"]
        self.script["download"] = restore
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            canonical = next(iter(self.worker.store.state["notes"].values()))
            self.assertEqual(set(collection.find_notes("")), {canonical, unrelated.id})
            self.assertNotIn(old_id, collection.find_notes(""))
            self.assertEqual(
                collection.get_note(unrelated.id)["Back"], "Unrelated answer"
            )
        self.assertNotIn("pendingDeletes", load_json(self.worker.store.state_path))

    def test_deleted_duplicate_stays_owned_after_remote_identity_edit(self):
        self.worker.once()
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(self.worker.store.state["notes"].values()))
            canonical = collection.get_note(nid)
            duplicate = collection.new_note(canonical.note_type())
            duplicate.fields = list(canonical.fields)
            collection.add_note(duplicate, self.worker.store.state["deckId"])
            other = collection.decks.id("Remote unrelated")
            unrelated = add_basic(collection, other)
            remote_backup = (
                self.worker.store.backup() / self.config.collection_path.name
            )
        with closing(Collection(str(remote_backup))) as remote:
            edited = remote.get_note(duplicate.id)
            edited["SourceID"] = "remote identity edit"
            remote.update_note(edited)
            remote.set_deck([edited.cards()[0].id], other)

        self.script["sync"] = [
            "accepted",
            TransientError("offline"),
            TransientError("offline"),
            TransientError("offline"),
        ]
        with self.assertRaises(TransientError):
            self.worker.once()

        def download(adapter):
            adapter.collection.close()
            adapter.store.config.collection_path.write_bytes(remote_backup.read_bytes())
            adapter.collection = Collection(str(adapter.store.config.collection_path))

        self.script["sync"] = ["download", "accepted"]
        self.script["download"] = download
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(set(collection.find_notes("")), {nid, unrelated.id})
            self.assertEqual(
                collection.get_note(unrelated.id)["Back"], "Unrelated answer"
            )

    def test_interrupted_bootstrap_can_delete_source_before_upload(self):
        self.script["sync"] = ["upload", "upload"]
        self.script["uploadError"] = TransientError("offline")
        with self.assertRaises(TransientError):
            self.worker.once()
        self.items = []
        del self.script["uploadError"]
        self.script["sync"] = ["upload", "upload"]
        self.assertTrue(self.worker.once()["healthy"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.note_count(), 0)

    def test_failed_upload_retains_mapping_and_restart_does_not_duplicate(self):
        def verify_mapping_then_fail(adapter):
            persisted = load_json(adapter.store.state_path)
            self.assertEqual(len(persisted["notes"]), 1)
            raise TransientError("network unavailable")

        self.script["sync"] = [
            "accepted",
            verify_mapping_then_fail,
            TransientError("network unavailable"),
            TransientError("network unavailable"),
        ]
        with self.assertRaises(TransientError):
            self.worker.once()
        state = load_json(self.worker.store.state_path)
        nid = next(iter(state["notes"].values()))
        self.assertNotIn("lastSuccess", load_json(self.worker.store.status_path))
        self.worker.once()
        self.assertEqual(
            next(iter(load_json(self.worker.store.state_path)["notes"].values())), nid
        )
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.note_count(), 1)
        self.assertEqual(self.sleeps, [1, 2])

    def test_concurrent_remote_edit_and_source_digest_change_converge(self):
        def mutate(adapter):
            note = adapter.collection.get_note(
                next(iter(adapter.store.state["notes"].values()))
            )
            note["Meaning"] = "concurrent remote edit"
            adapter.collection.update_note(note)
            self.items = [vocabulary(customDescription="latest source")]
            return "accepted"

        self.script["sync"] = ["accepted", mutate]
        result = self.worker.once()
        self.assertEqual(result["digest"], envelope(self.config, self.items)["digest"])
        with closing(Collection(str(self.config.collection_path))) as collection:
            note = collection.get_note(
                next(iter(load_json(self.worker.store.state_path)["notes"].values()))
            )
            self.assertEqual(note["Meaning"], "latest source")

    def test_unchanged_digest_still_repairs_remote_edit(self):
        self.worker.once()
        with closing(Collection(str(self.config.collection_path))) as collection:
            nid = next(iter(load_json(self.worker.store.state_path)["notes"].values()))
            note = collection.get_note(nid)
            note["Meaning"] = "remote edit"
            collection.update_note(note)
        result = self.worker.once()
        self.assertGreater(result["counts"]["updated"], 0)
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertIn("No definition saved", collection.get_note(nid)["Meaning"])

    def test_remote_contention_is_bounded_and_not_reported_as_success(self):
        self.script["remote"] = [True] * 4
        with self.assertRaisesRegex(TransientError, "bounded reconciliation"):
            self.worker.once()
        self.assertNotIn("lastSuccess", load_json(self.worker.store.status_path))

    def test_lock_identity_and_state_permissions(self):
        with (
            self.worker.store.lock(),
            self.assertRaisesRegex(WorkerError, "Another worker"),
        ):
            self.worker.once()
        self.worker.once()
        for key in ("username", "namespace", "owner"):
            other = Worker(replace(self.config, **{key: "different"}))
            with (
                self.subTest(key=key),
                self.assertRaisesRegex(WorkerError, "different account/source"),
            ):
                other.once()
        self.assertEqual(self.worker.store.state_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.worker.store.status_path.stat().st_mode & 0o777, 0o600)

    def test_status_requires_recent_remote_acceptance(self):
        self.assertFalse(inspect_status(self.config)["healthy"])
        self.worker.once()
        self.assertTrue(inspect_status(self.config)["healthy"])
        self.worker.store.status(
            lastSuccess=(datetime.now(timezone.utc) - timedelta(hours=1)).isoformat()
        )
        self.assertFalse(inspect_status(self.config)["healthy"])

    def test_stop_at_sync_boundary_closes_collection_and_starts_no_more_work(self):
        adapters = []

        def create(config, store):
            adapter = ControlledAdapter(config, store, self.script)
            adapters.append(adapter)
            return adapter

        def stop(adapter):
            self.worker.request_stop()
            return "accepted"

        self.worker.adapter_factory = create
        self.script["sync"] = [stop, "must not run"]
        with self.assertRaises(StopRequested):
            self.worker.once()
        self.assertEqual(self.script["sync"], ["must not run"])
        self.assertIsNone(adapters[0].collection.db)
        self.assertEqual(load_json(self.worker.store.status_path)["phase"], "stopped")
        with self.assertRaises(StopRequested):
            self.worker.once()
        self.assertEqual(len(adapters), 1)
        with closing(Collection(str(self.config.collection_path))) as collection:
            self.assertEqual(collection.note_count(), 0)

    def test_stop_interrupts_backoff_before_retry(self):
        attempts = []

        def offline():
            attempts.append(1)
            raise TransientError("offline")

        self.worker.sleep = lambda delay: self.worker.request_stop()
        with self.assertRaises(StopRequested):
            self.worker.retry(offline)
        self.assertEqual(attempts, [1])

    def test_cli_signals_interrupt_idle_wait_after_real_collection_cleanup(self):
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signal=signum):
                worker = Worker(
                    self.config,
                    adapter_factory=self.worker.adapter_factory,
                    fetch=self.worker.fetch,
                )
                adapters = []
                factory = worker.adapter_factory

                def create(config, store, factory=factory, adapters=adapters):
                    adapter = factory(config, store)
                    adapters.append(adapter)
                    return adapter

                worker.adapter_factory = create
                waiting = Event()
                wait = worker.wait

                def idle(delay, adapters=adapters, waiting=waiting, wait=wait):
                    self.assertIsNone(adapters[-1].collection.db)
                    waiting.set()
                    wait(300)

                def interrupt(waiting=waiting, signum=signum):
                    if waiting.wait(5):
                        os.kill(os.getpid(), signum)

                worker.wait = idle
                sender = Thread(target=interrupt)
                sender.start()
                started = time.monotonic()
                try:
                    with (
                        patch("sys.argv", ["anki_worker", "run"]),
                        patch(
                            "anki_worker.__main__.Config.from_env",
                            return_value=self.config,
                        ),
                        patch("anki_worker.__main__.Worker", return_value=worker),
                        patch("builtins.print"),
                    ):
                        self.assertEqual(main(), 0)
                finally:
                    sender.join(timeout=6)
                self.assertFalse(sender.is_alive())
                self.assertLess(time.monotonic() - started, 5)
                self.assertEqual(len(adapters), 1)
                self.assertTrue(worker.stop_event.is_set())


if __name__ == "__main__":
    unittest.main()
