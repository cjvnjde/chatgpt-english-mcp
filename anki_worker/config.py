import os
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlsplit


class WorkerError(Exception):
    """An operator-safe message, never raw transport or Anki error text."""


class TransientError(WorkerError):
    pass


def secret(name, *, required=True):
    value = os.environ.get(name, "")
    filename = os.environ.get(name + "_FILE", "")
    if value and filename:
        raise WorkerError(f"Set only {name} or {name}_FILE, not both")
    if filename:
        try:
            value = Path(filename).read_text(encoding="utf-8").rstrip("\r\n")
        except (OSError, UnicodeError):
            raise WorkerError(f"Cannot read {name}_FILE") from None
    if required and not value:
        raise WorkerError(f"Configure {name} or {name}_FILE")
    return value


@dataclass(frozen=True)
class Config:
    enabled: bool
    source_url: str
    token: str = field(repr=False)
    username: str = field(repr=False)
    password: str = field(repr=False)
    namespace: str = "english-mcp"
    owner: str = "default"
    deck: str = "English MCP"
    collection_path: Path = Path("/data/collection.anki2")
    poll_seconds: float = 60

    @property
    def identity(self):
        return {
            "username": self.username,
            "namespace": self.namespace,
            "owner": self.owner,
        }

    @classmethod
    def from_env(cls):
        enabled = os.environ.get("ANKI_SYNC_ENABLED", "false").lower()
        if enabled not in ("true", "false", "1", "0"):
            raise WorkerError("ANKI_SYNC_ENABLED must be true or false")
        active = enabled in ("true", "1")
        url = os.environ.get(
            "ANKI_SOURCE_URL", "http://english-learning-mcp:8082/internal/anki/snapshot"
        )
        parsed = urlsplit(url)
        if (
            parsed.scheme not in ("http", "https")
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.fragment
        ):
            raise WorkerError(
                "ANKI_SOURCE_URL must be an HTTP(S) URL without credentials or fragment"
            )
        token = secret("ANKI_EXPORT_TOKEN", required=active)
        if active and len(token.encode()) < 32:
            raise WorkerError("ANKI_EXPORT_TOKEN must contain at least 32 bytes")
        try:
            interval = float(os.environ.get("ANKI_POLL_SECONDS", "60"))
        except ValueError:
            raise WorkerError(
                "ANKI_POLL_SECONDS must be a finite number of at least 1"
            ) from None
        if not 1 <= interval <= 86400:
            raise WorkerError("ANKI_POLL_SECONDS must be between 1 and 86400")
        namespace = os.environ.get("ANKI_SOURCE_NAMESPACE", "english-mcp")
        owner = os.environ.get("MCP_OWNER_KEY", "default")
        deck = os.environ.get("ANKI_DECK", "English MCP")
        if (
            not namespace.strip()
            or not owner.strip()
            or not deck.strip()
            or any(c in deck for c in "\r\n\x00")
        ):
            raise WorkerError("Namespace, owner and deck must be nonempty valid names")
        return cls(
            active,
            url,
            token,
            secret("ANKIWEB_USERNAME", required=active),
            secret("ANKIWEB_PASSWORD", required=False),
            namespace,
            owner,
            deck,
            Path(
                os.environ.get("ANKI_COLLECTION_PATH", "/data/collection.anki2")
            ).absolute(),
            interval,
        )
