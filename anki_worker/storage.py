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

from .config import WorkerError


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
    def __init__(self, config):
        self.config = config
        self.directory = config.collection_path.parent
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.state_path = self.directory / "worker-state.json"
        self.auth_path = self.directory / "worker-auth.json"
        self.status_path = self.directory / "worker-status.json"
        self.state = None

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

    def backup(self):
        destination = (
            self.directory
            / "backups"
            / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
        )
        destination.mkdir(parents=True, mode=0o700)
        if self.config.collection_path.exists():
            with closing(
                sqlite3.connect(f"file:{self.config.collection_path}?mode=ro", uri=True)
            ) as source:
                target_path = destination / self.config.collection_path.name
                with closing(sqlite3.connect(target_path)) as target:
                    source.backup(target)
                os.chmod(target_path, 0o600)
        for path in (self.state_path, self.auth_path, self.status_path):
            if path.exists():
                shutil.copyfile(path, destination / path.name)
                os.chmod(destination / path.name, 0o600)
        return destination
