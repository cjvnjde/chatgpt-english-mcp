import { strict as assert } from "node:assert";
import { test } from "node:test";
import { dictionaryMedia } from "./dictionaryMedia.ts";
import type { DictionaryAudio, DictionaryLookup } from "./types.ts";

const audio = (mediaId: string): DictionaryAudio => ({
  audioUrl: `https://dictionary.example/${mediaId}.mp3`,
  contentType: "audio/mpeg",
  mediaId,
});

test("stored pronunciations render once per media object", () => {
  const uk = audio("uk-audio");
  const us = audio("us-audio");
  const lookup: DictionaryLookup = {
    entries: Array.from({ length: 3 }, (_, index) => ({
      headword: `foul-${index}`,
      audio: { uk, us },
      definitions: [],
    })),
    images: [],
  };
  lookup.entries.push({
    headword: "shared recording",
    audio: { uk: audio("shared-audio"), us: audio("shared-audio") },
    definitions: [],
  });

  assert.deepEqual(
    dictionaryMedia(lookup).audio.map(({ label, media }) => ({
      label,
      mediaId: media.mediaId,
    })),
    [
      { label: "UK", mediaId: "uk-audio" },
      { label: "US", mediaId: "us-audio" },
      { label: "UK / US", mediaId: "shared-audio" },
    ],
  );
});
