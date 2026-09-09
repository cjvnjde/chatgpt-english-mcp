#!/usr/bin/env python3
# Build-only dependency: uv run --no-project --with msgpack scripts/build_expressions.py
import argparse
import gzip
import hashlib
import io
import json
import tarfile
import urllib.request
from collections import Counter, defaultdict
from itertools import pairwise
from pathlib import Path

from build_usefulness import normalize

ROOT = Path(__file__).resolve().parents[1] / "internal" / "usefulness"
RESTRICTED = {"rare", "archaic", "obsolete", "dated"}


def digest_file(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def cached_input(cache, name, metadata, offline):
    path = cache / name
    if not path.exists():
        if offline:
            raise FileNotFoundError(f"Missing offline input: {path}")
        temporary = path.with_name(path.name + ".part")
        try:
            with (
                urllib.request.urlopen(metadata["url"], timeout=180) as response,
                temporary.open("wb") as destination,
            ):
                while chunk := response.read(1024 * 1024):
                    destination.write(chunk)
            digest = digest_file(temporary)
            if digest != metadata["sha256"]:
                raise ValueError(
                    f"SHA-256 mismatch for {path}: {digest}; restore the pinned snapshot"
                )
            temporary.replace(path)
            return path
        finally:
            temporary.unlink(missing_ok=True)
    digest = digest_file(path)
    if digest != metadata["sha256"]:
        raise ValueError(
            f"SHA-256 mismatch for {path}: {digest}; restore the pinned snapshot"
        )
    return path


def json_rows(path, compressed=False):
    opener = gzip.open if compressed else open
    with opener(path, "rb") as source:
        for number, line in enumerate(source, 1):
            try:
                row = json.loads(line)
            except (ValueError, UnicodeError) as error:
                raise ValueError(f"Malformed JSON in {path}, line {number}") from error
            if not isinstance(row, dict):
                raise TypeError(f"Expected an object in {path}, line {number}")
            yield row


def expression(term):
    if not isinstance(term, str):
        raise TypeError(f"Expected expression text, got {term!r}")
    normalized = normalize(term)
    return normalized if " " in normalized else ""


def new_entry(term):
    return {
        "term": term,
        "aliases": set(),
        "wiktionary": False,
        "wordnet": False,
        "restricted": False,
        "inflect_first": False,
        "idiomatic_documents": 0,
        "idiomatic_instances": 0,
        "literal_instances": 0,
        "wordnet_tags": 0,
    }


def entry_for(entries, term):
    if term not in entries:
        entries[term] = new_entry(term)
    return entries[term]


def add_alias(entry, text):
    alias = expression(text)
    if alias and alias != entry["term"]:
        entry["aliases"].add(alias)


def wiktionary_entries(path, entries):
    counts = Counter()
    restrictions = {}
    form_targets = defaultdict(set)
    independent = set()
    for row in json_rows(path, compressed=True):
        counts["rows"] += 1
        if row.get("lang_code") != "en":
            continue
        counts["english_rows"] += 1
        term = expression(row["word"])
        if not term:
            continue
        counts["multiword_rows"] += 1
        entry = entry_for(entries, term)
        entry["wiktionary"] = True
        if row.get("pos") == "verb":
            entry["inflect_first"] = True
        senses = row.get("senses", [])
        if not isinstance(senses, list):
            raise TypeError(f"Malformed Wiktionary senses: {term}")
        if not senses:
            restrictions[term] = False
            independent.add(term)
        for sense in senses:
            tags = set(row.get("tags", [])) | set(sense.get("tags", []))
            restricted = bool(tags & RESTRICTED)
            restrictions[term] = restrictions.get(term, True) and restricted
            # "common" can describe common law or regional preference, not
            # general frequency. Only explicit restrictive usage labels vote.
            targets = sense.get("form_of", []) + sense.get("alt_of", [])
            if not targets:
                independent.add(term)
            for target in targets:
                target_term = expression(target["word"])
                if target_term and target_term != term:
                    form_targets[term].add(target_term)
                else:
                    independent.add(term)
        for form in row.get("forms", []):
            tags = set(form.get("tags", []))
            if tags & {"romanization", "table-tags", "error-unrecognized-form"}:
                continue
            add_alias(entry, form["form"])
    if not counts["english_rows"] or not counts["multiword_rows"]:
        raise ValueError("Wiktionary source supplied no English multiword entries")
    for term, restricted in restrictions.items():
        entries[term]["restricted"] = restricted
    counts["normalized_terms"] = len(restrictions)
    return dict(counts), form_targets, independent


def observed_surface(row):
    context = row["context"]
    if not isinstance(context, list) or len(context) != 5:
        raise ValueError(f"Malformed MAGPIE context for id {row['id']}")
    text = context[2]
    offsets = row["offsets"]
    if not isinstance(text, str) or not isinstance(offsets, list) or not offsets:
        raise ValueError(f"Malformed MAGPIE offsets for id {row['id']}")
    for pair in offsets:
        if (
            not isinstance(pair, list)
            or len(pair) != 2
            or any(type(n) is not int for n in pair)
        ):
            raise ValueError(f"Malformed MAGPIE offset pair for id {row['id']}")
    offsets = sorted(offsets)
    # Verified against context[2]: Unicode character offsets, end exclusive.
    # Some upstream coordinates are invalid; never guess their intended span.
    for start, end in offsets:
        if not 0 <= start < end <= len(text):
            return "", "invalid_offsets"
        if (start and not text[start - 1].isspace()) or (
            end < len(text) and not text[end].isspace()
        ):
            return "", "invalid_offsets"
    for (_, end), (start, _) in pairwise(offsets):
        if end > start:
            return "", "invalid_offsets"
        if text[end:start].strip():
            return "", "noncontiguous_offsets"
    return text[offsets[0][0] : offsets[-1][1]], "contiguous_offsets"


def magpie_entries(path, entries):
    counts = Counter()
    documents = defaultdict(set)
    seen = set()
    types = set()
    for row in json_rows(path):
        counts["rows"] += 1
        identifier = row["id"]
        if identifier in seen:
            raise ValueError(f"Duplicate MAGPIE instance id: {identifier}")
        seen.add(identifier)
        term = expression(row["idiom"])
        if not term:
            counts["singleword_rows"] += 1
            continue
        types.add(term)
        confidence = row["confidence"]
        if type(confidence) not in (int, float) or not 0 <= confidence <= 1:
            raise ValueError(f"Invalid MAGPIE confidence: {identifier}")
        label = row["label"]
        if label not in {"i", "l", "f", "o", "?"}:
            raise ValueError(f"Unknown MAGPIE label: {label}")
        if confidence < 0.75 or label not in {"i", "l"}:
            counts["excluded_rows"] += 1
            continue
        entry = entry_for(entries, term)
        if label == "l":
            entry["literal_instances"] += 1
            counts["literal_instances"] += 1
            continue
        document = row["document_id"]
        if not isinstance(document, str) or not document:
            raise ValueError(f"Missing MAGPIE document id: {identifier}")
        documents[term].add(document)
        entry["idiomatic_instances"] += 1
        counts["idiomatic_instances"] += 1
        surface, status = observed_surface(row)
        counts[status] += 1
        if surface:
            add_alias(entry, surface)
    if not counts["idiomatic_instances"]:
        raise ValueError("MAGPIE source supplied no confident idiomatic instances")
    for term, ids in documents.items():
        entries[term]["idiomatic_documents"] = len(ids)
    counts["multiword_types"] = len(types)
    counts["attested_types"] = len(documents)
    counts["at_least_10_documents"] = sum(len(ids) >= 10 for ids in documents.values())
    return dict(counts)


def wordnet_entries(path, entries):
    counts = Counter()
    verb_forms = defaultdict(set)
    terms = set()
    seen_senses = set()
    with tarfile.open(path, "r:gz") as archive:
        for line in archive.extractfile("dict/index.sense"):
            fields = line.decode("utf-8").split()
            if len(fields) != 4:
                raise ValueError("Malformed WordNet index.sense row")
            sense, offset, number, tags = fields
            if sense in seen_senses:
                raise ValueError(f"Duplicate WordNet sense: {sense}")
            seen_senses.add(sense)
            lemma, sense_type = sense.split("%", 1)
            count = int(tags)
            if count < 0 or int(offset) < 0 or int(number) < 1:
                raise ValueError(f"Invalid WordNet sense counters: {sense}")
            counts["senses"] += 1
            term = expression(lemma.replace("_", " "))
            if not term:
                continue
            counts["multiword_senses"] += 1
            terms.add(term)
            entry = entry_for(entries, term)
            entry["wordnet"] = True
            entry["wordnet_tags"] += count
            if sense_type.startswith("2:"):
                entry["inflect_first"] = True
        for part in ("noun", "verb", "adj", "adv"):
            for line in archive.extractfile(f"dict/{part}.exc"):
                fields = line.decode("utf-8").split()
                if len(fields) < 2:
                    raise ValueError(f"Malformed WordNet {part} exception")
                counts["exception_rows"] += 1
                inflection = normalize(fields[0].replace("_", " "))
                for lemma in fields[1:]:
                    canonical = normalize(lemma.replace("_", " "))
                    if (
                        part == "verb"
                        and " " not in inflection
                        and " " not in canonical
                    ):
                        verb_forms[inflection].add(canonical)
                    if canonical in entries and " " in inflection:
                        add_alias(entries[canonical], inflection)
    if not terms:
        raise ValueError("WordNet source supplied no multiword entries")
    counts["normalized_terms"] = len(terms)
    counts["at_least_5_tags"] = sum(
        entries[term]["wordnet_tags"] >= 5 for term in terms
    )
    # Wiktionary provides full explicit regular inflections. Retain WordNet's
    # genuine exception ambiguity here rather than synthesizing morphology.
    return dict(counts), {
        form: sorted(lemmas) for form, lemmas in sorted(verb_forms.items())
    }


def canonicalize_forms(entries, targets, independent):
    collapsible = {}
    for term in targets:
        entry = entries[term]
        if (
            term in independent
            or entry["restricted"]
            or entry["wordnet"]
            or entry["idiomatic_instances"]
            or entry["literal_instances"]
        ):
            continue
        collapsible[term] = targets[term]

    resolved = {}
    for term in collapsible:
        current = term
        visited = set()
        while current in collapsible:
            if current in visited or len(collapsible[current]) != 1:
                current = ""
                break
            visited.add(current)
            current = next(iter(collapsible[current]))
        if current and current != term and current in entries:
            resolved[term] = current
    for term, canonical in resolved.items():
        entry = entries[canonical]
        entry["aliases"].add(term)
        entry["aliases"].update(entries[term]["aliases"])
    for term in resolved:
        del entries[term]
    for entry in entries.values():
        entry["aliases"].discard(entry["term"])
        entry["aliases"] = sorted(entry["aliases"])
    return len(resolved)


def legal_bytes(metadata, paths):
    path = paths[metadata["input"]]
    if "member" not in metadata:
        return path.read_bytes()
    with tarfile.open(path, "r:gz") as archive:
        content = archive.extractfile(metadata["member"]).read()
    if "prefix_lines" in metadata:
        content = b"".join(
            content.splitlines(keepends=True)[: metadata["prefix_lines"]]
        )
    return content


def main():
    parser = argparse.ArgumentParser(
        description="Build offline expression evidence from SHA-256-pinned cached sources"
    )
    parser.add_argument(
        "--cache-dir", type=Path, default=Path("/tmp/english-mcp-expressions")
    )
    parser.add_argument(
        "--manifest", type=Path, default=ROOT / "assets" / "expressions-manifest.json"
    )
    parser.add_argument("--output-dir", type=Path, default=ROOT)
    parser.add_argument(
        "--offline",
        action="store_true",
        help="Require every pinned input in the cache; never access the network",
    )
    args = parser.parse_args()
    pinned = json.loads(args.manifest.read_text(encoding="utf-8"))
    manifest = {key: pinned[key] for key in ("sources", "inputs", "legal_files")}
    args.cache_dir.mkdir(parents=True, exist_ok=True)
    paths = {
        name: cached_input(args.cache_dir, name, metadata, args.offline)
        for name, metadata in manifest["inputs"].items()
    }
    entries = {}
    wiktionary, targets, independent = wiktionary_entries(
        paths["kaikki.jsonl.gz"], entries
    )
    print("Wiktionary:", json.dumps(wiktionary), flush=True)
    magpie = magpie_entries(paths["magpie.jsonl"], entries)
    wordnet, verb_forms = wordnet_entries(paths["wordnet.tar.gz"], entries)
    collapsed = canonicalize_forms(entries, targets, independent)
    data = {
        "entries": [entries[term] for term in sorted(entries)],
        "verb_forms": verb_forms,
    }
    encoded = json.dumps(data, ensure_ascii=False, separators=(",", ":")).encode(
        "utf-8"
    )
    digest = hashlib.sha256(encoded).hexdigest()
    compressed = io.BytesIO()
    with gzip.GzipFile(
        filename="", mode="wb", fileobj=compressed, mtime=0, compresslevel=9
    ) as output:
        output.write(encoded)
    alias_targets = defaultdict(set)
    for entry in data["entries"]:
        for alias in entry["aliases"]:
            alias_targets[alias].add(entry["term"])
    manifest.update(
        {
            "format": "JSON entries and WordNet verb exception forms; gzip mtime=0",
            "normalization": "build_usefulness.normalize, matching domain.NormalizeTerm; WordNet underscores become spaces; all senses retained on normalized collisions",
            "dataset_sha256": digest,
            "terms": len(entries),
            "aliases": sum(len(entry["aliases"]) for entry in entries.values()),
            "ambiguous_aliases": sum(
                len(targets) > 1 for targets in alias_targets.values()
            ),
            "collapsed_form_entries": collapsed,
            "verb_forms": len(verb_forms),
            "restricted_terms": sum(entry["restricted"] for entry in entries.values()),
            "uncompressed_bytes": len(encoded),
            "compressed_bytes": len(compressed.getvalue()),
            "counts": {"wiktionary": wiktionary, "magpie": magpie, "wordnet": wordnet},
        }
    )
    legal_outputs = {}
    for name, metadata in manifest["legal_files"].items():
        content = legal_bytes(metadata, paths)
        digest_legal = hashlib.sha256(content).hexdigest()
        if digest_legal != metadata["sha256"]:
            raise ValueError(f"Legal file checksum mismatch: {name}")
        legal_outputs[name] = content
    assets = args.output_dir / "assets"
    assets.mkdir(parents=True, exist_ok=True)
    (assets / "expressions.json.gz").write_bytes(compressed.getvalue())
    (assets / "expressions-manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )
    (args.output_dir / "expression_revision.go").write_text(
        f'package usefulness\n\nconst expressionDatasetRevision = "{digest}"\n',
        encoding="utf-8",
    )
    for name, content in legal_outputs.items():
        path = args.output_dir / "licenses" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    print(
        json.dumps(
            {
                key: value
                for key, value in manifest.items()
                if key not in {"inputs", "legal_files"}
            },
            indent=2,
        )
    )
    for term in (
        "spill the beans",
        "kick the bucket",
        "break the ice",
        "at the end of the day",
        "rain cats and dogs",
        "take into account",
    ):
        print(json.dumps(entries.get(term), ensure_ascii=False))


if __name__ == "__main__":
    main()
