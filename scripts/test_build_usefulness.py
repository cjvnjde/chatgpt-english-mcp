import gzip
import io
import unittest
import zipfile

import msgpack
from build_usefulness import frequencywords_ranks, wordfreq_ranks


class RankSourceTests(unittest.TestCase):
    def test_frequencywords_preserves_unicode_whitespace_inside_terms(self):
        content = "the 20\r\nNew\u0085York 10\nnew\u2028york 9\nend 1\n".encode("utf-8")
        ranks, count = frequencywords_ranks(content)
        self.assertEqual(ranks, {"the": 1, "new york": 2, "end": 4})
        self.assertEqual(count, 4)

    def test_frequencywords_rejects_empty_source(self):
        with self.assertRaises(ValueError):
            frequencywords_ranks(b"")

    def test_wordfreq_rejects_malformed_or_empty_bins(self):
        header = {"format": "cB", "version": 1}
        for bins in ([], [header], [header, []], [header, "hello"], [header, [23]]):
            with self.subTest(bins=bins):
                content = io.BytesIO()
                with zipfile.ZipFile(content, "w") as wheel:
                    wheel.writestr(
                        "wordfreq/data/large_en.msgpack.gz",
                        gzip.compress(msgpack.packb(bins)),
                    )
                with (
                    zipfile.ZipFile(content) as wheel,
                    self.assertRaises((TypeError, ValueError)),
                ):
                    wordfreq_ranks(wheel)


if __name__ == "__main__":
    unittest.main()
