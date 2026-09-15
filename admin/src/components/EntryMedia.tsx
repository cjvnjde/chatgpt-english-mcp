import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  Show,
} from "solid-js";
import type { API } from "../api";
import type {
  DictionaryAudio,
  DictionaryImage,
  DictionaryLookup,
  Vocabulary,
  VocabularyImage,
} from "../types";

export default function EntryMedia(props: {
  api: API;
  item: Vocabulary;
  busy: boolean;
  setBusy: (value: boolean) => void;
  updated: (item: Vocabulary) => void;
  failed: (error: unknown) => void;
  changed: (message: string) => void;
}) {
  const [file, setFile] = createSignal<File>();
  const [example, setExample] = createSignal("");
  const [notice, setNotice] = createSignal("");
  const [confirming, setConfirming] = createSignal("");
  let input: HTMLInputElement | undefined;

  const upload = async () => {
    const image = file();
    if (!image || props.busy) return;
    props.setBusy(true);
    setNotice("");
    const body = new FormData();
    body.append("image", image, image.name);
    body.append("example", example());
    body.append("expectedRevision", String(props.item.revision));
    try {
      const item = await props.api<Vocabulary>(
        `/vocabulary/${encodeURIComponent(props.item.itemId)}/images`,
        { method: "POST", body },
      );
      props.updated(retainLoadedMedia(props.item, item));
      setFile(undefined);
      setExample("");
      if (input) input.value = "";
      setNotice("Image attached to this vocabulary item.");
      props.changed("Vocabulary image attached.");
    } catch (error) {
      props.failed(error);
    } finally {
      props.setBusy(false);
    }
  };

  const remove = async (image: VocabularyImage) => {
    if (props.busy || confirming() !== image.attachmentId) return;
    props.setBusy(true);
    setNotice("");
    try {
      const item = await props.api<Vocabulary>(
        `/vocabulary/${encodeURIComponent(props.item.itemId)}/images/${encodeURIComponent(image.attachmentId)}`,
        {
          method: "DELETE",
          body: JSON.stringify({ expectedRevision: props.item.revision }),
        },
      );
      props.updated(retainLoadedMedia(props.item, item));
      setConfirming("");
      setNotice("Image removed.");
      props.changed("Vocabulary image removed.");
    } catch (error) {
      props.failed(error);
    } finally {
      props.setBusy(false);
    }
  };

  const lookup = createMemo(() => props.item.lookup);
  const dictionary = createMemo(() => dictionaryMedia(lookup()));
  return (
    <section class="entry-media" aria-labelledby="entry-media-heading">
      <div class="section-heading">
        <div>
          <h3 id="entry-media-heading">Audio and images</h3>
          <p class="muted">
            Cambridge media is copied into this server when a lookup is fetched.
            Additional images belong only to this saved vocabulary item.
          </p>
        </div>
      </div>

      <Show
        when={dictionary().audio.length || dictionary().images.length}
        fallback={
          <p class="empty-inline">
            No locally stored Cambridge media is linked to this item yet.
          </p>
        }
      >
        <div class="dictionary-media">
          <For each={dictionary().audio}>
            {(audio) => (
              <article class="media-card audio-card">
                <strong>{audio.label} pronunciation</strong>
                <StoredMedia
                  api={props.api}
                  mediaId={audio.media.mediaId!}
                  kind="audio"
                  label={`${audio.label} pronunciation`}
                />
              </article>
            )}
          </For>
          <For each={dictionary().images}>
            {(image) => (
              <article class="media-card">
                <StoredMedia
                  api={props.api}
                  mediaId={(image.mediaId || image.thumbnailMediaId)!}
                  kind="image"
                  label={image.alt || image.title || "Cambridge illustration"}
                />
                <Show when={image.title || image.credit}>
                  <small>
                    {[image.title, image.credit].filter(Boolean).join(" · ")}
                  </small>
                </Show>
              </article>
            )}
          </For>
        </div>
      </Show>

      <div class="attachment-heading">
        <div>
          <strong>Additional example images</strong>
          <small>JPEG, PNG, GIF, WebP, or AVIF; up to 10 MiB each.</small>
        </div>
        <span>{props.item.images.length}/12</span>
      </div>
      <Show when={props.item.images.length}>
        <div class="attachment-grid">
          <For each={props.item.images}>
            {(image) => (
              <article class="media-card attachment-card">
                <StoredMedia
                  api={props.api}
                  mediaId={image.mediaId}
                  kind="image"
                  label={image.example || image.originalFilename || "Example image"}
                />
                <div>
                  <Show when={image.example}>
                    <p>{image.example}</p>
                  </Show>
                  <small>
                    {image.originalFilename || "Uploaded image"} · {formatBytes(image.byteSize)}
                  </small>
                </div>
                <Show
                  when={confirming() === image.attachmentId}
                  fallback={
                    <button
                      type="button"
                      class="text-button danger-text"
                      onClick={() => setConfirming(image.attachmentId)}
                    >
                      Remove
                    </button>
                  }
                >
                  <div class="attachment-remove">
                    <span>Remove this image?</span>
                    <button
                      type="button"
                      class="danger"
                      onClick={() => void remove(image)}
                    >
                      Remove image
                    </button>
                    <button type="button" onClick={() => setConfirming("")}>
                      Keep
                    </button>
                  </div>
                </Show>
              </article>
            )}
          </For>
        </div>
      </Show>
      <div class="image-upload">
        <label>
          Image file
          <input
            ref={input}
            type="file"
            accept="image/jpeg,image/png,image/gif,image/webp,image/avif"
            onChange={(event) => setFile(event.currentTarget.files?.[0])}
          />
        </label>
        <label>
          Example or memory cue
          <input
            maxlength="2000"
            value={example()}
            onInput={(event) => setExample(event.currentTarget.value)}
            placeholder="Optional sentence or reason this image helps"
          />
        </label>
        <button
          type="button"
          class="primary"
          disabled={!file() || props.item.images.length >= 12}
          onClick={() => void upload()}
        >
          Attach image
        </button>
      </div>
      <Show when={notice()}>
        <p class="media-notice" role="status">
          {notice()}
        </p>
      </Show>
    </section>
  );
}

function StoredMedia(props: {
  api: API;
  mediaId: string;
  kind: "audio" | "image";
  label: string;
}) {
  const [url, setURL] = createSignal("");
  const [failed, setFailed] = createSignal(false);
  createEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    setURL("");
    setFailed(false);
    void props
      .api<Blob>(
        `/media/${encodeURIComponent(props.mediaId)}`,
        { signal: controller.signal },
        "blob",
      )
      .then((blob) => {
        if (controller.signal.aborted) return;
        objectURL = URL.createObjectURL(blob);
        setURL(objectURL);
      })
      .catch((error) => {
        if (!(error instanceof DOMException && error.name === "AbortError"))
          setFailed(true);
      });
    onCleanup(() => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    });
  });
  return (
    <Show
      when={url()}
      fallback={
        <div class="media-placeholder" role={failed() ? "alert" : "status"}>
          {failed() ? "Stored media unavailable" : "Loading stored media…"}
        </div>
      }
    >
      {(source) => (
        <Show
          when={props.kind === "image"}
          fallback={<audio controls preload="metadata" src={source()} aria-label={props.label} />}
        >
          <img src={source()} alt={props.label} loading="lazy" />
        </Show>
      )}
    </Show>
  );
}

function retainLoadedMedia(current: Vocabulary, updated: Vocabulary): Vocabulary {
  const sameLookup =
    !!current.lookup?.lookupId &&
    current.lookup.lookupId === updated.lookup?.lookupId;
  return {
    ...updated,
    lookup: sameLookup ? current.lookup : updated.lookup,
    images: updated.images.map(
      (image) =>
        current.images.find(
          (candidate) => candidate.attachmentId === image.attachmentId,
        ) || image,
    ),
  };
}

function dictionaryMedia(lookup: DictionaryLookup | undefined): {
  audio: { label: "UK" | "US"; media: DictionaryAudio }[];
  images: DictionaryImage[];
} {
  const audio: { label: "UK" | "US"; media: DictionaryAudio }[] = [];
  const images = new Map<string, DictionaryImage>();
  const addImage = (image: DictionaryImage) => {
    const mediaId = image.mediaId || image.thumbnailMediaId;
    if (mediaId && !images.has(mediaId)) images.set(mediaId, image);
  };
  for (const image of lookup?.images || []) addImage(image);
  for (const entry of lookup?.entries || []) {
    if (entry.audio?.uk?.mediaId)
      audio.push({ label: "UK", media: entry.audio.uk });
    if (entry.audio?.us?.mediaId)
      audio.push({ label: "US", media: entry.audio.us });
    for (const definition of entry.definitions || [])
      for (const image of definition.images || []) addImage(image);
  }
  return { audio, images: [...images.values()] };
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
}
