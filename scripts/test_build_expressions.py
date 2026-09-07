import unittest

from build_expressions import canonicalize_forms, new_entry


class CanonicalizeFormsTests(unittest.TestCase):
    def test_form_chain_retains_intermediate_usage_evidence(self):
        entries = {
            term: new_entry(term)
            for term in ("upon the matter", "upon the whole matter", "on the whole")
        }
        entries["upon the whole matter"].update(wiktionary=True, restricted=True)
        entries["on the whole"].update(wordnet=True, wordnet_tags=4)
        targets = {
            "upon the matter": {"upon the whole matter"},
            "upon the whole matter": {"on the whole"},
        }

        canonicalize_forms(entries, targets, {"on the whole"})

        self.assertNotIn("upon the matter", entries)
        self.assertIn("upon the matter", entries["upon the whole matter"]["aliases"])
        self.assertNotIn("upon the matter", entries["on the whole"]["aliases"])
        self.assertTrue(entries["upon the whole matter"]["restricted"])


if __name__ == "__main__":
    unittest.main()
