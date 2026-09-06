import base64
import json
import re
from dataclasses import dataclass
from datetime import datetime
from urllib.error import HTTPError, URLError
from urllib.request import HTTPRedirectHandler, Request, build_opener

from .config import TransientError, WorkerError

# Optional keys mirror Go's omitempty. A list may be null because Go nil slices
# encode as null; the envelope's authoritative items list must never be null.
PRONUNCIATION = {"uk?": str, "us?": str}
IMAGE = {
    "title?": str,
    "alt?": str,
    "imageUrl": str,
    "thumbnailUrl?": str,
    "credit?": str,
}
GROUP = {"topic?": str, "words": [str]}
DEFINITION = {
    "definition": str,
    "examples": [str],
    "phrases": [str],
    "seeAlso": [str],
    "images": [IMAGE],
    "guideword?": str,
    "phraseTitle?": str,
    "labels": [str],
    "usages?": [{"phrase?": str, "example?": str}],
    "related?": [GROUP],
    "synonyms?": [GROUP],
    "antonyms?": [GROUP],
}
AUDIO = {"audioUrl": str, "contentType": str}
ENTRY = {
    "headword": str,
    "partOfSpeech?": str,
    "pronunciations": PRONUNCIATION,
    "audio?": {"uk?": AUDIO, "us?": AUDIO},
    "inflections?": [str],
    "definitions": [DEFINITION],
    "idioms?": [str],
}
VOCABULARY = {
    "itemId": str,
    "term": str,
    "normalizedTerm": str,
    "status": str,
    "tags": [str],
    "customDescription?": str,
    "descriptionSource?": {"title?": str, "url?": str},
    "notes": [str],
    "examples": [str],
    "createdAt": str,
    "updatedAt": str,
    "sense?": {
        "context?": str,
        "entryIndex": int,
        "definitionIndex": int,
        "headword": str,
        "partOfSpeech?": str,
        "pronunciations?": PRONUNCIATION,
        "definition": DEFINITION,
    },
    "lookup?": {
        "lookupId": str,
        "requestedTerm": str,
        "normalizedTerm": str,
        "cache": {"state": str, "fetchedAt": str},
        "source": {
            "provider": str,
            "sourceUrl?": str,
            "datasetVersion?": str,
            "parserVersion": int,
        },
        "status": int,
        "entries": [ENTRY],
        "suggestions": [str],
        "images": [IMAGE],
        "idioms?": [str],
        "collocations?": [{"phrase": str, "example?": str}],
    },
}


def validate_shape(value, spec):
    if isinstance(spec, dict):
        if not isinstance(value, dict):
            raise WorkerError("Snapshot contains a malformed object")
        allowed = {key.rstrip("?") for key in spec}
        if set(value) - allowed:
            raise WorkerError(
                "Snapshot contains unsupported fields; check schema version"
            )
        for key, child in spec.items():
            name = key.rstrip("?")
            if name not in value:
                if key.endswith("?"):
                    continue
                raise WorkerError("Snapshot is missing required vocabulary fields")
            validate_shape(value[name], child)
    elif isinstance(spec, list):
        if value is None:
            return
        if not isinstance(value, list):
            raise WorkerError("Snapshot contains a malformed list")
        for item in value:
            validate_shape(item, spec[0])
    elif type(value) is not spec:
        raise WorkerError("Snapshot contains an invalid field type")
    elif spec is str:
        try:
            value.encode("utf-8")
        except UnicodeError:
            raise WorkerError("Snapshot contains invalid Unicode") from None
        if "\x00" in value:
            raise WorkerError("Snapshot contains NUL text")


def source_id(namespace, owner, item_id):
    encoded_owner = base64.urlsafe_b64encode(owner.encode()).decode().rstrip("=")
    return f"{namespace}:{encoded_owner}:{item_id}"


@dataclass(frozen=True)
class Snapshot:
    digest: str
    items: dict


def validate_snapshot(payload, config):
    if not isinstance(payload, dict) or set(payload) != {
        "schemaVersion",
        "namespace",
        "owner",
        "digest",
        "itemCount",
        "complete",
        "items",
    }:
        raise WorkerError("Snapshot envelope is missing or unsupported")
    if (
        type(payload["schemaVersion"]) is not int
        or payload["schemaVersion"] != 1
        or payload["complete"] is not True
    ):
        raise WorkerError("Snapshot is incomplete or uses an unsupported schema")
    if payload["namespace"] != config.namespace or payload["owner"] != config.owner:
        raise WorkerError("Snapshot source identity does not match worker identity")
    if not isinstance(payload["digest"], str) or not re.fullmatch(
        r"[0-9a-f]{64}", payload["digest"]
    ):
        raise WorkerError("Snapshot digest is malformed")
    rows = payload["items"]
    if (
        not isinstance(rows, list)
        or type(payload["itemCount"]) is not int
        or payload["itemCount"] != len(rows)
    ):
        raise WorkerError("Snapshot item count or items list is invalid")
    items = {}
    for row in rows:
        validate_shape(row, {"sourceId": str, "vocabulary": VOCABULARY})
        item = row["vocabulary"]
        if (
            not item["itemId"].strip()
            or not item["term"].strip()
            or not item["normalizedTerm"].strip()
        ):
            raise WorkerError("Snapshot contains an empty item identity or term")
        if item["status"] not in ("new", "learning", "learned", "archived"):
            raise WorkerError("Snapshot contains an unknown vocabulary status")
        for key in ("createdAt", "updatedAt"):
            try:
                if (
                    datetime.fromisoformat(item[key].replace("Z", "+00:00")).tzinfo
                    is None
                ):
                    raise ValueError()
            except ValueError:
                raise WorkerError(
                    "Snapshot contains an invalid vocabulary timestamp"
                ) from None
        sense = item.get("sense")
        if sense and (sense["entryIndex"] < 0 or sense["definitionIndex"] < 0):
            raise WorkerError("Snapshot contains an invalid selected-sense index")
        expected = source_id(config.namespace, config.owner, item["itemId"])
        if row["sourceId"] != expected or expected in items:
            raise WorkerError("Snapshot contains a duplicate or mismatched source ID")
        items[expected] = item
    return Snapshot(
        payload["digest"],
        dict(sorted(items.items(), key=lambda pair: pair[1]["itemId"])),
    )


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # The bearer token is scoped to this exact private endpoint.
        raise WorkerError(
            "Snapshot endpoint redirected; configure the final source URL"
        )


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise WorkerError("Snapshot JSON contains duplicate object keys")
        result[key] = value
    return result


def fetch_snapshot(config):
    request = Request(
        config.source_url,
        headers={
            "Authorization": "Bearer " + config.token,
            "Accept": "application/json",
        },
    )
    try:
        with build_opener(NoRedirect()).open(request, timeout=30) as response:
            if response.status != 200:
                raise WorkerError("Snapshot endpoint returned an unexpected status")
            body = response.read(64 * 1024 * 1024 + 1)
            if len(body) > 64 * 1024 * 1024:
                raise WorkerError("Snapshot exceeds the 64 MiB safety limit")
        payload = json.loads(body, object_pairs_hook=unique_object)
    except HTTPError as error:
        if error.code in (408, 429, 500, 502, 503, 504):
            raise TransientError("Snapshot service temporarily unavailable") from None
        raise WorkerError(
            f"Snapshot request rejected (HTTP {error.code}); check export configuration"
        ) from None
    except (URLError, TimeoutError, ConnectionError):
        raise TransientError("Snapshot transport temporarily unavailable") from None
    except (ValueError, UnicodeError, RecursionError):
        raise WorkerError("Snapshot response is not valid JSON") from None
    return validate_snapshot(payload, config)
