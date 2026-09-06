import random
import time

from .adapter import AnkiAdapter
from .config import TransientError, WorkerError
from .reconcile import changed, reconcile
from .snapshot import fetch_snapshot
from .storage import Store, now


class Worker:
    def __init__(
        self,
        config,
        *,
        adapter_factory=AnkiAdapter,
        fetch=fetch_snapshot,
        sleep=time.sleep,
        jitter=random.random,
    ):
        self.config = config
        self.store = Store(config)
        self.adapter_factory = adapter_factory
        self.fetch = fetch
        self.sleep = sleep
        self.jitter = jitter

    def retry(self, operation):
        for attempt in range(3):
            try:
                return operation()
            except TransientError:
                if attempt == 2:
                    raise
                self.sleep(2**attempt + self.jitter())

    def download(self, adapter):
        backup = self.store.backup()
        self.store.status(recoveryBackup=str(backup))
        self.retry(lambda: adapter.full(upload=False))
        self.store.state["baseline"] = True
        self.store.state["bootstrap"] = False
        self.store.save()

    def unsafe_upload(self):
        backup = self.store.backup()
        raise WorkerError(
            f"Unsafe full upload refused; backup: {backup}. Sync a complete trusted desktop collection to AnkiWeb, then retry with this volume; never force-upload a rebuilt worker collection"
        )

    def prepare_remote(self, adapter):
        result = self.retry(adapter.sync)
        state = self.store.state
        if result in ("download", "full"):
            self.download(adapter)
        elif result == "upload":
            if (
                not state["baseline"]
                and adapter.collection.is_empty()
                and not state["notes"]
                and state["modelId"] is None
            ):
                # The official protocol identifies the remote as empty. Only a
                # brand-new worker may bootstrap it; later FULL_UPLOAD is unsafe.
                state["baseline"] = True
                state["bootstrap"] = True
                self.store.save()
            elif not state["bootstrap"]:
                self.unsafe_upload()
        elif result == "accepted" and not state["baseline"]:
            state["baseline"] = True
            state["bootstrap"] = adapter.collection.is_empty()
            self.store.save()

    def bootstrap_upload(self, adapter):
        state = self.store.state
        collection = adapter.collection
        if (
            not state["bootstrap"]
            or collection.note_count() != len(state["notes"])
            or collection.card_count() != len(state["notes"])
        ):
            self.unsafe_upload()
        # Only the server's explicit FULL_UPLOAD response authorizes this path;
        # FULL_SYNC never does. Persisted bootstrap survives interrupted uploads.
        # Do not retry a full upload blindly after an ambiguous network failure:
        # the next cycle must recheck the server's safe direction first.
        adapter.full(upload=True)
        state["bootstrap"] = False
        self.store.save()

    def login(self):
        with self.store.lock():
            self.store.load()
            adapter = self.adapter_factory(self.config, self.store)
            try:
                self.retry(lambda: adapter.authenticate(force=True))
            finally:
                adapter.close()
            return {"authenticated": True}

    def once(self):
        started = time.monotonic()
        with self.store.lock():
            adapter = None
            try:
                self.store.load()
                # No Anki collection open, login, sync, or mutation before the
                # entire authoritative response passes validation.
                snapshot = self.retry(lambda: self.fetch(self.config))
                self.store.status(
                    healthy=False, phase="syncing", lastAttempt=now(), error=None
                )
                adapter = self.adapter_factory(self.config, self.store)
                self.retry(adapter.authenticate)
                totals = {"created": 0, "updated": 0, "deleted": 0, "removedCards": 0}
                for _ in range(4):
                    self.prepare_remote(adapter)
                    counts = reconcile(adapter.collection, self.store, snapshot)
                    for key in totals:
                        totals[key] += counts[key]
                    result = self.retry(adapter.sync)
                    if result in ("download", "full"):
                        self.download(adapter)
                        snapshot = self.retry(lambda: self.fetch(self.config))
                        continue
                    if result == "upload":
                        self.bootstrap_upload(adapter)
                    # A normal sync can merge concurrent remote edits over local
                    # fields. Inspect real merged content, not a source-only hash.
                    repairs = reconcile(adapter.collection, self.store, snapshot)
                    for key in totals:
                        totals[key] += repairs[key]
                    latest = self.retry(lambda: self.fetch(self.config))
                    if (
                        changed(repairs)
                        or latest.digest != snapshot.digest
                        or self.retry(adapter.remote_changed)
                    ):
                        snapshot = latest
                        continue
                    self.store.state["bootstrap"] = False
                    self.store.save()
                    return self.store.status(
                        healthy=True,
                        phase="idle",
                        lastSuccess=now(),
                        digest=snapshot.digest,
                        itemCount=len(snapshot.items),
                        counts=totals,
                        durationSeconds=round(time.monotonic() - started, 3),
                        error=None,
                    )
                raise TransientError(
                    "Source or Anki content kept changing; bounded reconciliation exhausted, pending changes retained for the next cycle"
                )
            except WorkerError as error:
                self.store.status(
                    healthy=False,
                    phase="failed",
                    lastAttempt=now(),
                    error=str(error),
                    durationSeconds=round(time.monotonic() - started, 3),
                )
                raise
            except Exception:  # noqa: BLE001 - Persist failure without leaking third-party exception secrets.
                self.store.status(
                    healthy=False,
                    phase="failed",
                    lastAttempt=now(),
                    error="Unexpected worker failure; check pinned version and collection integrity (details redacted)",
                    durationSeconds=round(time.monotonic() - started, 3),
                )
                raise WorkerError(
                    "Unexpected worker failure; check pinned version and collection integrity (details redacted)"
                ) from None
            finally:
                if adapter is not None:
                    adapter.close()
