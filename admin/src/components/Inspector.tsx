import { For, Show } from "solid-js";
import type { Row } from "../types";
import { display, download, label, pretty } from "../format";
import Modal from "./Modal";

export default function Inspector(props: {
  row: Row;
  table: string;
  owner: string;
  close: () => void;
  related: (table: string, column: string, value: string) => void;
  edit: (id: string) => void;
}) {
  const itemID = () =>
    props.table === "vocabulary_items"
      ? props.row.id
      : props.row.vocabulary_item_id;
  return (
    <Modal
      title={display(props.row.term || props.row._term || label(props.table))}
      close={props.close}
      wide
    >
      <div class="dialog-body">
        <div class="toolbar">
          <Show when={itemID()}>
            <Show when={props.row.owner_key === props.owner}>
              <button onClick={() => props.edit(String(itemID()))}>
                Open vocabulary item
              </button>
            </Show>
            <button
              onClick={() =>
                props.related(
                  "review_attempts",
                  "vocabulary_item_id",
                  String(itemID()),
                )
              }
            >
              Review history
            </button>
            <button
              onClick={() =>
                props.related(
                  "learning_cards",
                  "vocabulary_item_id",
                  String(itemID()),
                )
              }
            >
              Learning card
            </button>
          </Show>
          <button
            onClick={() =>
              download(
                `${props.table}-${props.row.id || "record"}.json`,
                props.row,
              )
            }
          >
            Export record
          </button>
        </div>
        <dl class="record-fields">
          <For each={Object.entries(props.row)}>
            {([key, value]) => (
              <div>
                <dt>
                  {label(key)}
                  <small>{key}</small>
                </dt>
                <dd>
                  <Show
                    when={value !== null}
                    fallback={<span class="muted">NULL</span>}
                  >
                    <pre>{pretty(value)}</pre>
                  </Show>
                  <Show when={key === "lookup_id" && value}>
                    <button
                      class="text-button"
                      onClick={() =>
                        props.related(
                          "dictionary_snapshots",
                          "id",
                          String(value),
                        )
                      }
                    >
                      Open dictionary snapshot
                    </button>
                  </Show>
                </dd>
              </div>
            )}
          </For>
        </dl>
      </div>
    </Modal>
  );
}
