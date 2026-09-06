from html import escape
from urllib.parse import urlsplit

FIELDS = (
    "SourceID",
    "Word",
    "Meaning",
    "Examples",
    "Context",
    "PartOfSpeech",
    "Pronunciation",
    "Notes",
    "Source",
    "VocabularyStatus",
)
FRONT = '<div class="word">{{Word}}</div>{{#Pronunciation}}<div class="pronunciation">{{Pronunciation}}</div>{{/Pronunciation}}'
BACK = '{{FrontSide}}<hr id="answer">' + "".join(
    "{{#"
    + name
    + "}}<section><h3>"
    + label
    + "</h3>{{"
    + name
    + "}}</section>{{/"
    + name
    + "}}"
    for name, label in (
        ("Meaning", "Meaning"),
        ("Examples", "Examples"),
        ("Context", "Context"),
        ("PartOfSpeech", "Part of speech"),
        ("Notes", "Notes"),
        ("Source", "Source"),
    )
)
CSS = ".card { font-family: sans-serif; font-size: 20px; text-align: left; line-height: 1.5; } .word { font-size: 32px; font-weight: bold; } .pronunciation { opacity: .75; } section { margin: 1em 0; } h3 { font-size: .75em; opacity: .7; margin-bottom: .2em; } ul { padding-left: 1.4em; }"


def text(value):
    return (
        escape(value, quote=True)
        .replace("\r\n", "\n")
        .replace("\r", "\n")
        .replace("\n", "<br>")
    )


def paragraphs(values):
    return "".join("<p>" + text(value) + "</p>" for value in values if value.strip())


def attribution(title, url):
    label = title or url
    try:
        parsed = urlsplit(url)
        safe = (
            parsed.scheme.lower() in ("http", "https")
            and parsed.hostname
            and not parsed.username
            and not parsed.password
        )
    except ValueError:
        safe = False
    if safe and not any(ord(char) < 32 for char in url):
        return (
            '<a href="'
            + escape(url, quote=True)
            + '" rel="noreferrer">'
            + text(label)
            + "</a>"
        )
    return text(title) if title else ""


def render(source, item):
    sense = item.get("sense") or {}
    definition = sense.get("definition") or {}
    lookup = item.get("lookup") or {}
    meaning = text(item.get("customDescription", ""))
    if not meaning.strip():
        meaning = text(definition.get("definition", ""))
    if not meaning.strip():
        groups = []
        for entry in lookup.get("entries") or []:
            definitions = [
                value["definition"]
                for value in entry["definitions"] or []
                if value["definition"].strip()
            ]
            if definitions:
                heading = entry["headword"]
                if entry.get("partOfSpeech"):
                    heading += " — " + entry["partOfSpeech"]
                groups.append(
                    "<div><strong>"
                    + text(heading)
                    + "</strong>"
                    + paragraphs(definitions)
                    + "</div>"
                )
        meaning = "".join(groups) or "<em>No definition saved.</em>"
    examples = list(
        dict.fromkeys((item["examples"] or []) + (definition.get("examples") or []))
    )
    pronunciation = sense.get("pronunciations") or {}
    pronunciation_text = " · ".join(
        region.upper() + " " + pronunciation[region]
        for region in ("uk", "us")
        if pronunciation.get(region)
    )
    description_source = item.get("descriptionSource") or {}
    dictionary_source = lookup.get("source") or {}
    sources = []
    if description_source:
        sources.append(
            attribution(
                description_source.get("title", ""), description_source.get("url", "")
            )
        )
    if dictionary_source:
        sources.append(
            attribution(
                dictionary_source["provider"], dictionary_source.get("sourceUrl", "")
            )
        )
    fields = [
        text(source),
        text(item["term"]),
        meaning,
        paragraphs(examples),
        text(sense.get("context", "")),
        text(sense.get("partOfSpeech", "")),
        text(pronunciation_text),
        paragraphs(item["notes"] or []),
        "<br>".join(dict.fromkeys(value for value in sources if value)),
        text(item["status"]),
    ]
    # Hex remains injective under Anki's case-insensitive tag canonicalization.
    # Unlike slugging/base64, spaces, punctuation and case cannot collapse tags.
    tags = sorted(
        {"vocab::u" + tag.encode("utf-8").hex() for tag in item["tags"] or []}
    )
    return fields, tags
