import os
from importlib.metadata import version
from urllib.parse import urlsplit

from anki.collection import Collection
from anki.errors import AnkiException, NetworkError, SyncError, SyncErrorKind
from anki.sync import SyncAuth, SyncOutput, SyncStatus

from .config import TransientError, WorkerError
from .storage import atomic_json, load_json

ANKI_VERSION = "26.8.1"


class AnkiAdapter:
    """Only this boundary knows the pinned library's authentication/sync API."""

    def __init__(self, config, store):
        if version("anki") != ANKI_VERSION:
            raise WorkerError(
                "Unsupported Anki library version; install anki==" + ANKI_VERSION
            )
        self.config = config
        self.store = store
        self.collection = Collection(str(config.collection_path))
        os.chmod(config.collection_path, 0o600)
        self.auth = None

    def close(self):
        self.collection.close()

    def call(self, operation):
        try:
            return operation()
        except NetworkError:
            raise TransientError(
                "AnkiWeb network request failed; retrying with bounded backoff"
            ) from None
        except SyncError as error:
            if error.kind is SyncErrorKind.AUTH:
                self.auth = None
                self.store.auth_path.unlink(missing_ok=True)
                raise WorkerError(
                    "AnkiWeb authentication rejected; renew credentials and run login"
                ) from None
            raise WorkerError(
                "AnkiWeb rejected synchronization; check service availability and pinned client/server version before retrying"
            ) from None
        except AnkiException:
            raise WorkerError(
                "Anki operation failed; back up the volume and check collection integrity/client version"
            ) from None

    def authenticate(self, *, force=False):
        saved = None if force else load_json(self.store.auth_path)
        if saved is not None:
            if (
                not isinstance(saved, dict)
                or saved.get("identity") != self.config.identity
                or not isinstance(saved.get("hkey"), str)
                or not saved["hkey"]
            ):
                raise WorkerError(
                    "Stored Anki authentication has the wrong identity or format; run login"
                )
            endpoint = saved.get("endpoint") or None
            self.check_endpoint(endpoint)
            self.auth = SyncAuth(
                hkey=saved["hkey"], endpoint=endpoint, io_timeout_secs=60
            )
            return
        if not self.config.password:
            raise WorkerError(
                "Configure ANKIWEB_PASSWORD or ANKIWEB_PASSWORD_FILE, then run login"
            )
        self.auth = self.call(
            lambda: self.collection.sync_login(
                self.config.username, self.config.password, None
            )
        )
        self.auth.io_timeout_secs = 60
        self.save_auth()

    @staticmethod
    def check_endpoint(endpoint):
        if endpoint is None:
            return
        if not isinstance(endpoint, str):
            raise WorkerError("Stored AnkiWeb endpoint is malformed; run login")
        parsed = urlsplit(endpoint)
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or not (
                parsed.hostname == "ankiweb.net"
                or parsed.hostname.endswith(".ankiweb.net")
            )
        ):
            raise WorkerError(
                "AnkiWeb returned an unexpected sync host; verify the official endpoint before upgrading the adapter"
            )

    def save_auth(self):
        self.check_endpoint(self.auth.endpoint or None)
        atomic_json(
            self.store.auth_path,
            {
                "identity": self.config.identity,
                "hkey": self.auth.hkey,
                "endpoint": self.auth.endpoint,
            },
        )

    def accept_endpoint(self, result):
        if result.new_endpoint:
            self.check_endpoint(result.new_endpoint)
            self.auth.endpoint = result.new_endpoint
            self.save_auth()

    def sync(self):
        result = self.call(
            lambda: self.collection.sync_collection(self.auth, sync_media=False)
        )
        self.accept_endpoint(result)
        if result.required == SyncOutput.NO_CHANGES:
            return "accepted"
        if result.required == SyncOutput.FULL_DOWNLOAD:
            return "download"
        if result.required == SyncOutput.FULL_UPLOAD:
            return "upload"
        if result.required == SyncOutput.FULL_SYNC:
            return "full"
        raise WorkerError(
            "Unsupported Anki sync response; check the pinned library/server version"
        )

    def full(self, *, upload):
        self.collection.close_for_full_sync()
        try:
            self.call(
                lambda: self.collection.full_upload_or_download(
                    auth=self.auth, server_usn=None, upload=upload
                )
            )
        finally:
            self.collection.reopen(after_full_sync=True)

    def remote_changed(self):
        result = self.call(lambda: self.collection.sync_status(self.auth))
        self.accept_endpoint(result)
        return result.required != SyncStatus.NO_CHANGES
