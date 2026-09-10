import random
import time
from threading import Event

from .adapter import AnkiAdapter
from .config import StopRequested, TransientError, WorkerError
from .reconcile import changed, reconcile
from .snapshot import fetch_snapshot
from .storage import Store, now


class Worker:
    def __init__(
        self,
        config,
        *,
        adapter_factory=AnkiAdapter,
        fetch=None,
        sleep=None,
        jitter=random.random,
    ):
        self.config = config
        self.stop_event = Event()
        self.store = Store(config, stop_event=self.stop_event)
        self.adapter_factory = adapter_factory
        self.fetch = fetch or (
            lambda config: fetch_snapshot(config, check_stop=self.check_stop)
        )
        self.sleep = sleep
        self.jitter = jitter

    def request_stop(self):
        self.stop_event.set()

    def check_stop(self):
        if self.stop_event.is_set():
            raise StopRequested()

    def wait(self, seconds):
        self.check_stop()
        if self.sleep is None:
            self.stop_event.wait(seconds)
        else:
            self.sleep(seconds)
        self.check_stop()

    def retry(self, operation):
        for attempt in range(3):
            self.check_stop()
            try:
                result = operation()
                self.check_stop()
                return result
            except TransientError:
                if attempt == 2:
                    raise
                self.wait(2**attempt + self.jitter())

    def download(self, adapter):
        self.check_stop()
        self.store.recovery_backup()
        self.retry(lambda: adapter.full(upload=False))
        self.store.state["baseline"] = True
        self.store.state["bootstrap"] = False
        self.store.save()

    def unsafe_upload(self):
        backup = self.store.recovery_backup()
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
        note_ids = set(collection.find_notes(""))
        if (
            not state["bootstrap"]
            or not note_ids.issubset(state["notes"].values())
            or collection.card_count() != len(note_ids)
        ):
            self.unsafe_upload()
        # Only the server's explicit FULL_UPLOAD response authorizes this path;
        # FULL_SYNC never does. Persisted bootstrap survives interrupted uploads.
        # Do not retry a full upload blindly after an ambiguous network failure:
        # the next cycle must recheck the server's safe direction first.
        self.check_stop()
        adapter.full(upload=True)
        self.check_stop()
        state["bootstrap"] = False
        self.store.save()

    def login(self):
        self.check_stop()
        with self.store.lock():
            self.store.load()
            self.check_stop()
            adapter = self.adapter_factory(self.config, self.store)
            try:
                self.retry(lambda: adapter.authenticate(force=True))
            finally:
                adapter.close()
            return {"authenticated": True}

    def once(self):
        started = time.monotonic()
        self.check_stop()
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
                self.check_stop()
                adapter = self.adapter_factory(self.config, self.store)
                self.retry(adapter.authenticate)
                totals = {"created": 0, "updated": 0, "deleted": 0, "removedCards": 0}
                for _ in range(4):
                    self.prepare_remote(adapter)
                    self.check_stop()
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
                    self.check_stop()
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
                    self.check_stop()
                    self.store.state["bootstrap"] = False
                    self.store.state["notes"] = {
                        source: self.store.state["notes"][source]
                        for source in snapshot.items
                    }
                    self.store.state.pop("pendingDeletes", None)
                    self.store.save()
                    self.store.finish_recovery()
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
            except StopRequested:
                self.store.status(healthy=False, phase="stopped", error=None)
                raise
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
