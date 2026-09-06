"""Explicit destructive integration exercise; never discovered by unittest.

Run python -m anki_worker.live --disposable-account EXACT_ANKIWEB_USERNAME
with ANKI_LIVE_DISPOSABLE_ACCOUNT=I_UNDERSTAND_THIS_ACCOUNT_WILL_BE_MODIFIED.
The account must start empty. Ordinary worker configuration provides secrets.
"""

import argparse
import hashlib
import json
import os
import tempfile
from dataclasses import replace
from pathlib import Path

from .adapter import AnkiAdapter
from .config import Config, WorkerError
from .rendering import render
from .snapshot import source_id, validate_snapshot
from .storage import Store
from .worker import Worker

ACKNOWLEDGEMENT = "I_UNDERSTAND_THIS_ACCOUNT_WILL_BE_MODIFIED"


def require(condition, message):
    if not condition:
        raise WorkerError("Live integration assertion failed: " + message)


def sync_client(adapter):
    result = adapter.sync()
    if result in ("download", "full"):
        adapter.store.backup()
        adapter.full(upload=False)
    elif result != "accepted":
        raise WorkerError(
            "Live client requires unsafe full upload; stop and inspect the disposable account manually"
        )


def item(item_id):
    return {
        "itemId": item_id,
        "term": "bank",
        "normalizedTerm": "bank",
        "status": "archived",
        "usefulness": "normal",
        "tags": ["live test", "Case", "case"],
        "customDescription": "Original meaning",
        "notes": ["Disposable account integration exercise"],
        "examples": [],
        "createdAt": "2026-09-01T00:00:00Z",
        "updatedAt": "2026-09-01T00:00:00Z",
    }


def exercise(config, root):
    probe_config = replace(config, collection_path=root / "probe" / "collection.anki2")
    probe_store = Store(probe_config)
    with probe_store.lock():
        probe_store.load()
        probe = AnkiAdapter(probe_config, probe_store)
        try:
            probe.authenticate()
            result = probe.sync()
            if result in ("download", "full"):
                probe_store.backup()
                probe.full(upload=False)
                result = "accepted"
            if result not in ("accepted", "upload") or not probe.collection.is_empty():
                raise WorkerError(
                    "Live test requires a new empty disposable AnkiWeb account; no source mutations were performed"
                )
        finally:
            probe.close()

    worker_config = replace(
        config, collection_path=root / "worker" / "collection.anki2"
    )
    items = [item("first-sense"), item("second-sense")]

    def fetch(current_config):
        payload = {
            "schemaVersion": 2,
            "namespace": current_config.namespace,
            "owner": current_config.owner,
            "digest": hashlib.sha256(
                json.dumps(items, sort_keys=True).encode()
            ).hexdigest(),
            "itemCount": len(items),
            "complete": True,
            "items": [
                {
                    "sourceId": source_id(
                        current_config.namespace, current_config.owner, value["itemId"]
                    ),
                    "vocabulary": value,
                }
                for value in items
            ],
        }
        return validate_snapshot(payload, current_config)

    worker = Worker(worker_config, fetch=fetch)
    worker.once()
    original_mapping = dict(worker.store.state["notes"])
    first_source = source_id(config.namespace, config.owner, "first-sense")
    second_source = source_id(config.namespace, config.owner, "second-sense")

    client_config = replace(
        config, collection_path=root / "client" / "collection.anki2"
    )
    client_store = Store(client_config)
    with client_store.lock():
        client_store.load()
        client = AnkiAdapter(client_config, client_store)
        try:
            client.authenticate()
            sync_client(client)
            collection = client.collection
            first = collection.get_note(original_mapping[first_source])
            original_card = first.cards()[0]
            unrelated_deck = collection.decks.id("Live unrelated deck")
            unrelated = collection.new_note(collection.models.by_name("Basic"))
            unrelated["Front"] = "Unrelated content must survive"
            unrelated["Back"] = "Preserved"
            collection.add_note(unrelated, unrelated_deck)
            original_card.reps = 9
            original_card.ivl = 21
            original_card.type = 2
            original_card.queue = 2
            original_card.due = 35
            collection.update_card(original_card)
            collection.set_deck([original_card.id], unrelated_deck)
            first["Meaning"] = "Remote edit must be repaired"
            first["SourceID"] = "Remote identity edit"
            first.tags = ["remote"]
            collection.update_note(first)
            collection.remove_notes([original_mapping[second_source]])
            manual = collection.new_note(collection.models.by_name("Basic"))
            manual["Front"] = "Manual managed-deck addition"
            collection.add_note(manual, worker.store.state["deckId"])
            require(
                client.sync() == "accepted", "remote client edits must use normal sync"
            )

            items[0]["customDescription"] = "Latest source meaning"
            worker = Worker(worker_config, fetch=fetch)
            worker.once()
            sync_client(client)
            restored = collection.get_note(original_mapping[first_source])
            card = restored.cards()[0]
            require(
                restored.fields == render(first_source, items[0])[0],
                "source fields overwrite remote fields",
            )
            require(
                (card.id, card.reps, card.ivl, card.due)
                == (original_card.id, 9, 21, 35),
                "card identity and scheduling survive",
            )
            require(
                card.did == worker.store.state["deckId"],
                "moved card returns to managed deck",
            )
            require(
                worker.store.state["notes"][second_source]
                != original_mapping[second_source],
                "deleted note is recreated",
            )
            require(
                manual.id not in collection.find_notes(""),
                "manual managed note is removed",
            )
            require(
                collection.get_note(unrelated.id)["Back"] == "Preserved",
                "unrelated content survives",
            )

            items.clear()
            worker.once()
            sync_client(client)
            require(
                collection.find_notes("") == [unrelated.id],
                "empty source clears only managed content",
            )
            # Initialize another worker against this now-existing account. Keep
            # the managed object IDs from the original volume: this is a recovery
            # exercise, not unauthorized takeover by display name.
            restored_config = replace(
                config, collection_path=root / "restored" / "collection.anki2"
            )
            restored_store = Store(restored_config)
            restored_store.load()
            restored_store.state.update(
                deckId=worker.store.state["deckId"],
                modelId=worker.store.state["modelId"],
            )
            restored_store.save()
            items.append(item("after-download"))
            Worker(restored_config, fetch=fetch).once()
            sync_client(client)
            require(
                collection.note_count() == 2
                and collection.get_note(unrelated.id)["Back"] == "Preserved",
                "initial existing-account download preserves unrelated content",
            )
        finally:
            client.close()
    return {
        "liveIntegration": "passed",
        "ankiVersion": "26.8.1",
        "localArtifacts": str(root),
        "verified": [
            "headless login",
            "empty bootstrap",
            "existing-account full download",
            "create/update/delete",
            "remote repair",
            "restart",
            "scheduling preservation",
            "unrelated content preservation",
        ],
    }


def main():
    parser = argparse.ArgumentParser(
        description="Destructive live verification against a NEW EMPTY disposable AnkiWeb account"
    )
    parser.add_argument(
        "--disposable-account",
        required=True,
        help="Exact configured AnkiWeb username, acknowledging this test modifies it",
    )
    args = parser.parse_args()
    try:
        config = Config.from_env()
        if (
            os.environ.get("ANKI_LIVE_DISPOSABLE_ACCOUNT") != ACKNOWLEDGEMENT
            or args.disposable_account != config.username
        ):
            raise WorkerError(
                "Refusing live test without explicit disposable-account acknowledgement and matching username"
            )
        if not config.enabled:
            raise WorkerError("Live test requires ANKI_SYNC_ENABLED=true")
        root = Path(tempfile.mkdtemp(prefix="english-mcp-anki-live-"))
        try:
            result = exercise(config, root)
        except Exception:  # noqa: BLE001 - Integration logs must not expose account credentials.
            print(
                json.dumps(
                    {
                        "healthy": False,
                        "error": "Live integration failed; credentials/error details redacted",
                        "localArtifacts": str(root),
                    }
                )
            )
            return 1
        print(json.dumps(result, sort_keys=True))
        return 0
    except WorkerError as error:
        print(json.dumps({"healthy": False, "error": str(error)}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
