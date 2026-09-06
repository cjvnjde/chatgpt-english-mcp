from copy import deepcopy
from html import unescape
from uuid import uuid4

from .config import WorkerError
from .rendering import BACK, CSS, FIELDS, FRONT, render, text
from .snapshot import source_id

MODEL_NAME = "English MCP Vocabulary"


def refuse_schema(store, reason):
    backup = store.backup()
    raise WorkerError(
        reason
        + f"; backup: {backup}. Restore the managed note type/card structure in Anki and sync again; do not delete the worker state"
    )


def pending_name(store, key):
    if not store.state.get(key):
        # Persist an unguessable temporary name before Anki allocates its ID.
        # Restart can recover that object without claiming an unrelated name.
        store.state[key] = "English MCP pending " + uuid4().hex
        store.save()
    return store.state[key]


def ensure_structure(collection, store):
    state = store.state
    deck = (
        collection.decks.get(state["deckId"], default=False)
        if state["deckId"]
        else None
    )
    model = collection.models.get(state["modelId"]) if state["modelId"] else None
    if deck is None and state.get("pendingDeck"):
        deck = collection.decks.by_name(state["pendingDeck"])
        if deck:
            state["deckId"] = int(deck["id"])
            store.save()
    if model is None and state.get("pendingModel"):
        model = collection.models.by_name(state["pendingModel"])
        if model:
            state["modelId"] = int(model["id"])
            store.save()
    named_deck = collection.decks.by_name(store.config.deck)
    named_model = collection.models.by_name(MODEL_NAME)
    if named_deck and (deck is None or named_deck["id"] != deck["id"]):
        raise WorkerError(
            "Configured managed deck name is already owned by an untracked deck; rename that deck or configure an unused name"
        )
    if model is None and named_model:
        raise WorkerError(
            "Managed note-type name already exists without matching persisted ownership; restore matching worker state or rename the unrelated type"
        )
    if deck and deck.get("dyn"):
        refuse_schema(store, "Managed deck was converted to a filtered deck")
    if model and (
        model["type"] != 0
        or len(model["flds"]) != len(FIELDS)
        or len(model["tmpls"]) != 1
    ):
        refuse_schema(
            store, "Managed note type has incompatible fields or card templates"
        )

    changed = False
    if deck is None:
        state["deckId"] = int(
            collection.decks.add_normal_deck_with_name(
                pending_name(store, "pendingDeck")
            ).id
        )
        store.save()
        deck = collection.decks.get(state["deckId"])
        changed = True
    if deck["name"] != store.config.deck:
        collection.decks.rename(deck["id"], store.config.deck)
        changed = True
    if state.pop("pendingDeck", None):
        store.save()

    source_index = 0
    if model is None:
        model = collection.models.new(pending_name(store, "pendingModel"))
        for name in FIELDS:
            collection.models.add_field(model, collection.models.new_field(name))
        template = collection.models.new_template("Recognition")
        template.update(qfmt=FRONT, afmt=BACK)
        collection.models.add_template(model, template)
        model["css"] = CSS
        model["did"] = state["deckId"]
        state["modelId"] = int(collection.models.add_dict(model).id)
        store.save()
        model = collection.models.get(state["modelId"])
        model["name"] = MODEL_NAME
        collection.models.update_dict(model)
        changed = True
    else:
        source_index = next(
            (
                index
                for index, field in enumerate(model["flds"])
                if field["name"] == "SourceID"
            ),
            0,
        )
        desired = deepcopy(model)
        if state.get("pendingModel"):
            desired["name"] = MODEL_NAME
        for field, name in zip(desired["flds"], FIELDS):
            field["name"] = name
        desired["css"] = CSS
        desired["did"] = state["deckId"]
        desired["tmpls"][0].update(name="Recognition", qfmt=FRONT, afmt=BACK, did=None)
        if desired != model:
            collection.models.update_dict(desired)
            model = collection.models.get(state["modelId"])
            changed = True
    if state.pop("pendingModel", None):
        store.save()
    return model, source_index, changed


def reconcile(collection, store, snapshot):
    model, source_index, structure_changed = ensure_structure(collection, store)
    state = store.state
    deck_id = state["deckId"]
    model_id = state["modelId"]
    mapping = state["notes"]
    tracked = {nid: source for source, nid in mapping.items()}
    prefix = text(source_id(store.config.namespace, store.config.owner, ""))
    expected_fields = {text(source): source for source in snapshot.items}
    by_source = {}
    all_notes = {}
    cards_by_note = {}
    # SQL is read-only indexing; every mutation goes through Anki's supported API.
    deck_notes = set(
        collection.db.list(
            "select distinct nid from cards where did = ? or odid = ?", deck_id, deck_id
        )
    )
    for nid, mid in collection.db.all("select id, mid from notes"):
        if nid not in tracked and mid != model_id and nid not in deck_notes:
            continue
        cards = [collection.get_card(cid) for cid in collection.card_ids_of_note(nid)]
        note = collection.get_note(nid)
        all_notes[nid] = note
        cards_by_note[nid] = cards
        if nid in tracked:
            if mid != model_id:
                refuse_schema(
                    store, "A tracked note was converted to another note type"
                )
            source = tracked[nid]
        elif mid == model_id:
            value = note.fields[source_index]
            source = expected_fields.get(value)
            if source is None and value.startswith(prefix):
                # Recover creations whose SQLite commit preceded the mapping write.
                source = unescape(value)
        else:
            source = None
        if source:
            by_source.setdefault(source, []).append(nid)

    # Exceptional tracked/shared notes are refused before any note/card mutation.
    for source, nids in by_source.items():
        for nid in nids:
            cards = cards_by_note[nid]
            if len(cards) > 1:
                refuse_schema(
                    store,
                    "A managed note has multiple cards; separate unrelated cards into their own note before retrying",
                )

    counts = {
        "created": 0,
        "updated": 0,
        "deleted": 0,
        "removedCards": 0,
        "structureChanged": structure_changed,
    }
    keep = set()
    for source, item in snapshot.items.items():
        candidates = by_source.get(source, [])
        preferred = mapping.get(source)
        nid = preferred if preferred in candidates else min(candidates, default=None)
        desired_fields, desired_tags = render(source, item)
        if nid is None:
            note = collection.new_note(model)
            note.fields = desired_fields
            note.tags = desired_tags
            collection.add_note(note, deck_id)
            nid = int(note.id)
            # Durable identity precedes every remote upload. A crash before this
            # write is recovered above from the private model's SourceID field.
            mapping[source] = nid
            store.save()
            counts["created"] += 1
        else:
            note = all_notes[nid]
            changed = note.fields != desired_fields or sorted(note.tags) != desired_tags
            if changed:
                note.fields = desired_fields
                note.tags = desired_tags
                collection.update_note(note)
            cards = cards_by_note[nid]
            if not cards:
                collection.after_note_updates(
                    [nid], mark_modified=False, generate_cards=True
                )
                changed = True
            elif cards[0].did != deck_id or cards[0].odid:
                collection.set_deck([cards[0].id], deck_id)
                changed = True
            counts["updated"] += int(changed)
            mapping[source] = nid
        keep.add(nid)

    owned = {nid for nids in by_source.values() for nid in nids}
    for nid, note in all_notes.items():
        if nid in keep:
            continue
        cards = cards_by_note[nid]
        managed_cards = [
            card.id for card in cards if card.did == deck_id or card.odid == deck_id
        ]
        if nid in owned or (managed_cards and len(managed_cards) == len(cards)):
            collection.remove_notes([nid])
            counts["deleted"] += 1
        elif managed_cards:
            # remove_notes_by_card removes entire notes: NEVER use it here.
            collection.remove_cards_and_orphaned_notes(managed_cards)
            counts["removedCards"] += len(managed_cards)
    state["notes"] = {source: mapping[source] for source in snapshot.items}
    store.save()
    return counts


def changed(counts):
    return any(counts.values())
