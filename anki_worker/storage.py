import fcntl
import json
import os
import re
import shutil
import sqlite3
import tempfile
from contextlib import closing, contextmanager
from datetime import datetime, timezone
from pathlib import Path

from .config import StopRequested, WorkerError


def now():
    return datetime.now(timezone.utc).isoformat()


def atomic_json(path, payload):
    path = Path(path)
    descriptor, temporary = tempfile.mkstemp(
        prefix="." + path.name + ".", dir=path.parent
    )
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            os.fchmod(stream.fileno(), 0o600)
            json.dump(payload, stream, ensure_ascii=False, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def load_json(path, default=None):
    if not path.exists():
        return default
    try:
        os.chmod(path, 0o600)
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError, UnicodeError):
        raise WorkerError(
            "Worker state is unreadable; restore collection and state together from backup"
        ) from None


class Store:
    BACKUP_LIMIT = 5

    def __init__(self, config, *, stop_event=None):
        self.config = config
        self.directory = config.collection_path.parent
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.state_path = self.directory / "worker-state.json"
        self.auth_path = self.directory / "worker-auth.json"
        self.status_path = self.directory / "worker-status.json"
        self.state = None
        self.stop_event = stop_event
        self.recovery_path = self.directory / "worker-recovery.json"

    def check_stop(self):
        if self.stop_event is not None and self.stop_event.is_set():
            raise StopRequested()

    @contextmanager
    def lock(self):
        descriptor = os.open(
            self.directory / "worker.lock", os.O_CREAT | os.O_RDWR, 0o600
        )
        try:
            os.fchmod(descriptor, 0o600)
            try:
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise WorkerError(
                    "Another worker owns this account volume; run only one worker per AnkiWeb account"
                ) from None
            yield
        finally:
            os.close(descriptor)

    def load(self):
        self.state = load_json(self.state_path)
        if self.state is None:
            if self.config.collection_path.exists():
                raise WorkerError(
                    "Untracked local collection found; use a new empty worker volume or restore its matching state"
                )
            self.state = {
                "version": 1,
                "identity": self.config.identity,
                "notes": {},
                "deckId": None,
                "modelId": None,
                "baseline": False,
                "bootstrap": False,
            }
            self.save()
        state = self.state
        if (
            not isinstance(state, dict)
            or state.get("version") != 1
            or state.get("identity") != self.config.identity
        ):
            raise WorkerError(
                "Worker volume belongs to a different account/source or unsupported state version; use its original identity"
            )
        if not isinstance(state.get("notes"), dict) or any(
            not isinstance(source, str) or type(nid) is not int or nid <= 0
            for source, nid in state["notes"].items()
        ):
            raise WorkerError(
                "Worker note mapping is malformed; restore matching state and collection backup"
            )
        if len(set(state["notes"].values())) != len(state["notes"]):
            raise WorkerError(
                "Worker note mapping contains conflicting identities; restore backup"
            )
        pending_deletes = state.get("pendingDeletes", [])
        if not isinstance(pending_deletes, list) or any(
            type(nid) is not int or nid <= 0 for nid in pending_deletes
        ):
            raise WorkerError(
                "Worker pending deletion identity is malformed; restore backup"
            )
        if any(
            state.get(key) is not None
            and (type(state[key]) is not int or state[key] <= 0)
            for key in ("deckId", "modelId")
        ):
            raise WorkerError("Worker deck/model identity is malformed; restore backup")
        if any(type(state.get(key)) is not bool for key in ("baseline", "bootstrap")):
            raise WorkerError("Worker sync baseline state is malformed; restore backup")
        for key in ("pendingDeck", "pendingModel"):
            pending = state.get(key)
            if pending is not None and (
                not isinstance(pending, str)
                or not re.fullmatch(r"English MCP pending [0-9a-f]{32}", pending)
            ):
                raise WorkerError(
                    "Worker pending structure identity is malformed; restore backup"
                )
        return state

    def save(self):
        atomic_json(self.state_path, self.state)

    def status(self, **values):
        previous = load_json(self.status_path, {})
        previous.update(values)
        atomic_json(self.status_path, previous)
        return previous

    def recovery_backup(self):
        incident = load_json(self.recovery_path)
        if incident is not None and not isinstance(incident, dict):
            raise WorkerError(
                "Recovery backup identity is malformed; restore the matching recovery record"
            )
        if incident is None:
            incident = {
                "backup": datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
            }
            atomic_json(self.recovery_path, incident)
        name = incident.get("backup")
        if not isinstance(name, str) or not re.fullmatch(r"\d{8}T\d{6}\.\d{6}Z", name):
            raise WorkerError(
                "Recovery backup identity is malformed; restore the matching recovery record"
            )
        destination = self.directory / "backups" / name
        if not (destination / "complete.json").exists():
            self.backup(name=name)
        else:
            self.prune_backups()
        self.status(recoveryBackup=str(destination), recoveryActive=True)
        return destination

    def finish_recovery(self):
        self.recovery_path.unlink(missing_ok=True)
        self.sync_directory(self.directory)
        # Keep the last useful backup location in operator-visible status.
        self.status(recoveryActive=False)

    @staticmethod
    def sync_directory(path):
        descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def prune_backups(self):
        root = self.directory / "backups"
        incident = load_json(self.recovery_path, {})
        protected = incident.get("backup")
        completed = sorted(
            path
            for path in root.iterdir()
            if path.is_dir()
            and not path.is_symlink()
            and re.fullmatch(r"\d{8}T\d{6}\.\d{6}Z", path.name)
        )
        excess = len(completed) - self.BACKUP_LIMIT
        for path in completed:
            if excess <= 0:
                break
            if path.name != protected:
                self.check_stop()
                shutil.rmtree(path)
                excess -= 1
        self.sync_directory(root)

    def backup(self, *, name=None):
        self.check_stop()
        root = self.directory / "backups"
        root.mkdir(mode=0o700, exist_ok=True)
        os.chmod(root, 0o700)
        # An interrupted staging copy is never a completed recovery baseline.
        for path in root.iterdir():
            if re.fullmatch(r"\.partial-\d{8}T\d{6}\.\d{6}Z", path.name):
                shutil.rmtree(path)
        name = name or datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
        destination = root / name
        temporary = root / (".partial-" + name)
        temporary.mkdir(mode=0o700)
        try:
            if self.config.collection_path.exists():
                with closing(
                    sqlite3.connect(
                        self.config.collection_path.absolute().as_uri() + "?mode=ro",
                        uri=True,
                    )
                ) as source:
                    target_path = temporary / self.config.collection_path.name
                    descriptor = os.open(
                        target_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600
                    )
                    os.close(descriptor)
                    with closing(sqlite3.connect(target_path)) as target:
                        source.backup(
                            target,
                            pages=256,
                            progress=lambda status, remaining, total: self.check_stop(),
                        )
            for path in (self.state_path, self.auth_path, self.status_path):
                self.check_stop()
                if path.exists():
                    target_path = temporary / path.name
                    descriptor = os.open(
                        target_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600
                    )
                    with (
                        path.open("rb") as source,
                        os.fdopen(descriptor, "wb") as target,
                    ):
                        while block := source.read(1024 * 1024):
                            self.check_stop()
                            target.write(block)
            for path in temporary.iterdir():
                with path.open("rb") as stream:
                    os.fsync(stream.fileno())
            atomic_json(temporary / "complete.json", {"completedAt": now()})
            self.check_stop()
            os.rename(temporary, destination)
            self.sync_directory(root)
        finally:
            if temporary.exists():
                shutil.rmtree(temporary)
        self.prune_backups()
        return destination
