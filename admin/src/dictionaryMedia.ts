import type {
  DictionaryAudio,
  DictionaryImage,
  DictionaryLookup,
} from "./types";

export type StoredDictionaryAudio = {
  label: string;
  media: DictionaryAudio;
};

export function dictionaryMedia(lookup: DictionaryLookup | undefined): {
  audio: StoredDictionaryAudio[];
  images: DictionaryImage[];
} {
  const audio = new Map<
    string,
    { labels: Set<"UK" | "US">; media: DictionaryAudio }
  >();
  const images = new Map<string, DictionaryImage>();
  const addAudio = (label: "UK" | "US", media: DictionaryAudio | undefined) => {
    if (!media?.mediaId) return;
    const stored = audio.get(media.mediaId);
    if (stored) {
      stored.labels.add(label);
      return;
    }
    audio.set(media.mediaId, { labels: new Set([label]), media });
  };
  const addImage = (image: DictionaryImage) => {
    const mediaId = image.mediaId || image.thumbnailMediaId;
    if (mediaId && !images.has(mediaId)) images.set(mediaId, image);
  };

  for (const image of lookup?.images || []) addImage(image);
  for (const entry of lookup?.entries || []) {
    addAudio("UK", entry.audio?.uk);
    addAudio("US", entry.audio?.us);
    for (const definition of entry.definitions || [])
      for (const image of definition.images || []) addImage(image);
  }

  return {
    audio: [...audio.values()].map(({ labels, media }) => ({
      label: [...labels].join(" / "),
      media,
    })),
    images: [...images.values()],
  };
}
