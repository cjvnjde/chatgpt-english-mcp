import argparse
import json
import time
from datetime import datetime, timezone

from .config import Config, TransientError, WorkerError
from .storage import Store, load_json
from .worker import Worker


def inspect_status(config):
    status = load_json(
        Store(config).status_path, {"healthy": False, "phase": "never-synced"}
    )
    if not config.enabled:
        return {"healthy": False, "phase": "disabled"}
    try:
        age = (
            datetime.now(timezone.utc) - datetime.fromisoformat(status["lastSuccess"])
        ).total_seconds()
        if age < 0 or age > max(300, config.poll_seconds * 3):
            status = dict(status, healthy=False, phase="stale")
    except (KeyError, TypeError, ValueError):
        status = dict(status, healthy=False)
    return status


def main():
    parser = argparse.ArgumentParser(
        description="One-way authoritative vocabulary synchronization to AnkiWeb"
    )
    parser.add_argument("command", choices=("run", "once", "status", "login"))
    args = parser.parse_args()
    try:
        config = Config.from_env()
        if args.command == "status":
            status = inspect_status(config)
            print(json.dumps(status, sort_keys=True))
            return 0 if status.get("healthy") else 1
        if not config.enabled:
            raise WorkerError(
                "Anki synchronization is disabled; set ANKI_SYNC_ENABLED=true"
            )
        worker = Worker(config)
        if args.command == "login":
            print(json.dumps(worker.login()))
            return 0
        if args.command == "once":
            print(json.dumps(worker.once(), sort_keys=True))
            return 0
        while True:
            delay = config.poll_seconds
            try:
                print(json.dumps(worker.once(), sort_keys=True), flush=True)
            except WorkerError as error:
                print(json.dumps({"healthy": False, "error": str(error)}), flush=True)
                if not isinstance(error, TransientError):
                    delay = max(delay, 300)
            time.sleep(delay)
    except WorkerError as error:
        print(json.dumps({"healthy": False, "error": str(error)}), flush=True)
        return 1
    except KeyboardInterrupt:
        return 130
    except Exception:  # noqa: BLE001 - CLI boundary must not print credentials from third-party exceptions.
        print(
            json.dumps(
                {
                    "healthy": False,
                    "error": "Worker command failed (details redacted); check configuration and pinned Anki version",
                }
            ),
            flush=True,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
