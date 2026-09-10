#!/usr/bin/env python3
# Build-only: uv run --no-project --with-requirements .github/requirements-builders.txt scripts/build_usefulness.py
# Inputs are pinned by release/commit and SHA-256; no app Python dependencies.
import gzip
import hashlib
import io
import json
import struct
import urllib.request
import zipfile
from pathlib import Path

import msgpack

ROOT = Path(__file__).resolve().parents[1] / "internal" / "usefulness"
WORDFREQ_VERSION = "3.1.1"
WORDFREQ_URL = (
    "https://files.pythonhosted.org/packages/24/61/"
    "62835c475d69872d30689f284497853fe33fe1d6dd18f57346d13305861d/"
    "wordfreq-3.1.1-py3-none-any.whl"
)
WORDFREQ_SHA256 = "4b1c6ecffc6198be3396d5cf871c4423ca71c907c231348d352dd54d62b97473"
FREQUENCYWORDS_COMMIT = "072bbed282316a23651aa7068c7173aa7898cf80"
FREQUENCYWORDS_ROOT = (
    "https://raw.githubusercontent.com/hermitdave/FrequencyWords/"
    + FREQUENCYWORDS_COMMIT
    + "/"
)
FREQUENCYWORDS_SHA256 = (
    "7fea67ab954e2c01df6c608c9826e594cf36f8823b3243554f88245fb75dc506"
)
QUOTES = str.maketrans({"“": '"', "”": '"', "‘": "'", "’": "'"})
# Match Go's unicode.IsSpace, excluding Python-only C0 information separators.
WHITESPACE = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"


def download(url, expected_sha256=None):
    with urllib.request.urlopen(url, timeout=120) as response:
        content = response.read()
    digest = hashlib.sha256(content).hexdigest()
    if expected_sha256 is not None and digest != expected_sha256:
        raise ValueError(f"SHA-256 mismatch for {url}: {digest}")
    return content


def normalize(term):
    # Go strings.ToLower uses simple per-rune mappings, not Python's contextual
    # final sigma or expanding dotted-I mapping. Quote folding matches domain.
    lowered = "".join(character.lower()[0] for character in term)
    spaced = "".join(
        " " if character in WHITESPACE else character for character in lowered
    )
    return " ".join(part for part in spaced.split(" ") if part).translate(QUOTES)


def wordfreq_ranks(wheel):
    stored = wheel.read("wordfreq/data/large_en.msgpack.gz")
    bins = msgpack.unpackb(gzip.decompress(stored), raw=False)
    if not isinstance(bins, list) or not bins:
        raise ValueError("Expected wordfreq frequency bins")
    if bins[0] != {"format": "cB", "version": 1}:
        raise ValueError(f"Unexpected wordfreq header: {bins[0]!r}")
    ranks = {}
    count = 0
    for words in bins[1:]:
        if not isinstance(words, list):
            raise TypeError("Expected a wordfreq word list")
        # Standard competition ranking: equal-frequency terms share the first
        # position of their bin; later groups still count every original row.
        group_rank = count + 1
        for word in words:
            if not isinstance(word, str):
                raise TypeError(f"Expected wordfreq text, got {word!r}")
            count += 1
            term = normalize(word)
            if not term:
                raise ValueError("Empty normalized wordfreq term")
            ranks.setdefault(term, group_rank)
    if not count:
        raise ValueError("Wordfreq source supplied no terms")
    return ranks, count, hashlib.sha256(stored).hexdigest()


def frequencywords_ranks(content):
    ranks = {}
    previous_count = None
    group_rank = 0
    count = 0
    # Only actual line endings delimit records. Other Unicode whitespace can
    # occur inside a term and must reach normalize rather than split a row.
    for count, line in enumerate(io.StringIO(content.decode("utf-8"), newline=None), 1):
        word, occurrences_text = line.rsplit(" ", 1)
        occurrences = int(occurrences_text)
        if occurrences <= 0 or (
            previous_count is not None and occurrences > previous_count
        ):
            raise ValueError(f"FrequencyWords not descending at row {count}")
        if occurrences != previous_count:
            group_rank = count
        previous_count = occurrences
        term = normalize(word)
        if not term:
            raise ValueError(f"Empty normalized FrequencyWords term at row {count}")
        # Equal counts share their group's first rank, independent of row order.
        ranks.setdefault(term, group_rank)
    if not count:
        raise ValueError("FrequencyWords source supplied no terms")
    return ranks, count


def encode_ranks(wordfreq, frequencywords):
    terms = sorted(wordfreq.keys() | frequencywords.keys())
    records = bytearray(struct.pack("<I", len(terms)))
    text = bytearray()
    for term in terms:
        records.extend(
            struct.pack(
                "<III", len(text), wordfreq.get(term, 0), frequencywords.get(term, 0)
            )
        )
        text.extend(term.encode("utf-8"))
    return bytes(records + text), len(terms)


def main():
    with zipfile.ZipFile(io.BytesIO(download(WORDFREQ_URL, WORDFREQ_SHA256))) as wheel:
        wordfreq, wordfreq_count, wordfreq_data_sha256 = wordfreq_ranks(wheel)
        wordfreq_licenses = {
            name: wheel.read(f"wordfreq-{WORDFREQ_VERSION}.dist-info/{name}")
            for name in ("LICENSE.txt", "METADATA")
        }
    frequencywords_url = FREQUENCYWORDS_ROOT + "content/2018/en/en_full.txt"
    frequencywords, frequencywords_count = frequencywords_ranks(
        download(frequencywords_url, FREQUENCYWORDS_SHA256)
    )
    encoded, term_count = encode_ranks(wordfreq, frequencywords)
    digest = hashlib.sha256(encoded).hexdigest()
    compressed = io.BytesIO()
    with gzip.GzipFile(
        filename="", mode="wb", fileobj=compressed, mtime=0, compresslevel=9
    ) as output:
        output.write(encoded)

    assets = ROOT / "assets"
    assets.mkdir(parents=True, exist_ok=True)
    (assets / "ranks.bin.gz").write_bytes(compressed.getvalue())
    (ROOT / "revision.go").write_text(
        f'package usefulness\n\nconst datasetRevision = "{digest}"\n',
        encoding="utf-8",
    )
    manifest = {
        "format": "little-endian uint32 count; count records of text offset, wordfreq rank, FrequencyWords rank; UTF-8 text",
        "normalization": "domain.NormalizeTerm: simple lowercase, whitespace collapse, curly quote folding; best tied-group rank on collisions",
        "ranking": "standard competition ranking: equal-frequency entries share the first position of their group",
        "dataset_sha256": digest,
        "terms": term_count,
        "uncompressed_bytes": len(encoded),
        "compressed_bytes": len(compressed.getvalue()),
        "wordfreq": {
            "version": WORDFREQ_VERSION,
            "url": WORDFREQ_URL,
            "sha256": WORDFREQ_SHA256,
            "data_sha256": wordfreq_data_sha256,
            "original_rows": wordfreq_count,
            "normalized_terms": len(wordfreq),
        },
        "FrequencyWords": {
            "commit": FREQUENCYWORDS_COMMIT,
            "url": frequencywords_url,
            "sha256": FREQUENCYWORDS_SHA256,
            "original_rows": frequencywords_count,
            "normalized_terms": len(frequencywords),
        },
    }
    (assets / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )

    # Preserve upstream legal files and license context byte-for-byte.
    wordfreq_legal = ROOT / "licenses" / "wordfreq"
    wordfreq_legal.mkdir(parents=True, exist_ok=True)
    for name, content in wordfreq_licenses.items():
        (wordfreq_legal / name).write_bytes(content)
    frequencywords_legal = ROOT / "licenses" / "FrequencyWords"
    frequencywords_legal.mkdir(parents=True, exist_ok=True)
    for upstream_name, local_name in (
        ("LICENSE", "LICENSE"),
        ("README.md", "README.upstream"),
    ):
        (frequencywords_legal / local_name).write_bytes(
            download(FREQUENCYWORDS_ROOT + upstream_name)
        )

    print(json.dumps(manifest, indent=2))
    for term in (
        "the",
        "hello",
        "bank",
        "apple",
        "book",
        "beautiful",
        "meticulous",
        "sesquipedalian",
        "defenestration",
        "new york",
        "the and",
        "zzzxqvnotaword",
    ):
        print(
            f"{term!r}: wordfreq={wordfreq.get(term, 0)}, FrequencyWords={frequencywords.get(term, 0)}"
        )


if __name__ == "__main__":
    main()
